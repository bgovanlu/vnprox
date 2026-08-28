// SPDX-License-Identifier: Apache-2.0

// Command soak is T-607's churn-and-sample harness: a real HTTP client
// driving a real running vnproxd (booted against testdata/clusters/
// scale-lab.yaml, per docs/development.md's dev-loop pattern —
// testdata/dev-scale.toml) with continuous read-path traffic plus
// repeated real changeset churn, while sampling RSS (from
// /proc/<pid>/status), goroutine count (from the debug endpoint
// cmd/vnproxd/debugpprof.go adds when VNPROX_DEBUG_PPROF_ADDR is set), and
// the SQLite DB file size, at a fixed interval, to a CSV.
//
// This lives under testdata/ (ignored by `go build ./...`/golangci-lint's
// package discovery, same reasoning as testdata/genscale/main.go) — it is
// a one-off verification tool, not a shipped binary.
//
// Usage (see planning/reports/T-607.md for the actual invocation used and
// its results):
//
//	go run ./testdata/genscale/soak \
//	  --base https://127.0.0.1:28007 --pid <vnproxd-pid> \
//	  --pprof-addr 127.0.0.1:28009 --db var/dev-scale-vnprox.db \
//	  --duration 90m --sample-interval 60s --out /tmp/soak-samples.csv
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	base := flag.String("base", "https://127.0.0.1:28007", "vnproxd base URL")
	pid := flag.Int("pid", 0, "vnproxd process PID (for /proc/<pid>/status RSS sampling)")
	pprofAddr := flag.String("pprof-addr", "", "VNPROX_DEBUG_PPROF_ADDR value the daemon was started with, for goroutine-count sampling (empty = skip)")
	dbPath := flag.String("db", "", "path to the daemon's SQLite DB file, for size sampling (empty = skip)")
	duration := flag.Duration("duration", 90*time.Minute, "total soak duration")
	sampleInterval := flag.Duration("sample-interval", 60*time.Second, "RSS/goroutine/DB-size sample interval")
	readInterval := flag.Duration("read-interval", 5*time.Second, "read-path churn interval (GET topology/findings/audit)")
	draftInterval := flag.Duration("draft-interval", 45*time.Second, "draft-changeset churn interval (create+validate+delete, no apply)")
	realApplyInterval := flag.Duration("real-apply-interval", 20*time.Minute, "interval between real create->apply->confirm->reverse->apply->confirm cycles (bounded churn of the full lifecycle)")
	out := flag.String("out", "/tmp/soak-samples.csv", "CSV output path")
	flag.Parse()

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // soak harness against a throwaway dev cert
	}

	sessionID, csrfToken, err := login(client, *base, "root@pam", "vnprox-mock")
	if err != nil {
		log.Fatalf("soak: login: %v", err)
	}
	log.Printf("soak: logged in, session established")

	csvFile, err := os.Create(*out)
	if err != nil {
		log.Fatalf("soak: creating %s: %v", *out, err)
	}
	defer func() { _ = csvFile.Close() }()
	if _, err := fmt.Fprintln(csvFile, "elapsed_s,unix_ts,rss_kb,goroutines,db_bytes,reads_done,drafts_done,real_cycles_done"); err != nil {
		log.Fatalf("soak: writing CSV header: %v", err)
	}

	deadline := time.Now().Add(*duration)
	start := time.Now()
	nextRead := time.Now()
	nextDraft := time.Now()
	nextSample := time.Now()
	nextRealCycle := time.Now().Add(*realApplyInterval) // first real cycle after one full interval, not immediately
	var reads, drafts, realCycles int
	draftCounter := 0

	for time.Now().Before(deadline) {
		now := time.Now()
		if !now.Before(nextRead) {
			if err := doReads(client, *base, sessionID); err != nil {
				log.Printf("soak: read-path churn error (non-fatal): %v", err)
			} else {
				reads++
			}
			nextRead = now.Add(*readInterval)
		}
		if !now.Before(nextDraft) {
			draftCounter++
			if err := churnDraft(client, *base, sessionID, csrfToken, draftCounter); err != nil {
				log.Printf("soak: draft churn error (non-fatal): %v", err)
			} else {
				drafts++
			}
			nextDraft = now.Add(*draftInterval)
		}
		if !now.Before(nextRealCycle) {
			if err := realApplyCycle(client, *base, sessionID, csrfToken, realCycles); err != nil {
				log.Printf("soak: real apply cycle error (non-fatal): %v", err)
			} else {
				realCycles++
				log.Printf("soak: completed real apply/confirm/reverse cycle #%d", realCycles)
			}
			nextRealCycle = now.Add(*realApplyInterval)
		}
		if !now.Before(nextSample) {
			rssKB := sampleRSS(*pid)
			goroutines := sampleGoroutines(*pprofAddr)
			dbBytes := sampleDBSize(*dbPath)
			elapsed := now.Sub(start).Seconds()
			line := fmt.Sprintf("%.0f,%d,%d,%d,%d,%d,%d,%d", elapsed, now.Unix(), rssKB, goroutines, dbBytes, reads, drafts, realCycles)
			if _, err := fmt.Fprintln(csvFile, line); err != nil {
				log.Printf("soak: writing sample: %v", err)
			}
			if err := csvFile.Sync(); err != nil {
				log.Printf("soak: syncing CSV: %v", err)
			}
			log.Printf("soak: sample t=%.0fs rss=%dKB goroutines=%d db=%dB reads=%d drafts=%d realCycles=%d",
				elapsed, rssKB, goroutines, dbBytes, reads, drafts, realCycles)
			nextSample = now.Add(*sampleInterval)
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("soak: complete after %s: %d reads, %d draft churns, %d real apply cycles", duration.String(), reads, drafts, realCycles)
}

func login(client *http.Client, base, username, password string) (sessionID, csrfToken string, err error) {
	body := fmt.Sprintf(`{"username":%q,"password":%q,"realm":"pam"}`, username, password)
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("login: status %d: %s", resp.StatusCode, b)
	}
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "vnprox_session":
			sessionID = c.Value
		case "vnprox_csrf":
			csrfToken = c.Value
		}
	}
	if sessionID == "" || csrfToken == "" {
		return "", "", fmt.Errorf("login: no session/csrf cookie in response")
	}
	return sessionID, csrfToken, nil
}

func newAuthedRequest(method, url, sessionID, csrfToken string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "vnprox_session", Value: sessionID})
	if csrfToken != "" {
		req.Header.Set("X-VNPROX-CSRF", csrfToken)
	}
	return req, nil
}

func doReads(client *http.Client, base, sessionID string) error {
	for _, path := range []string{"/api/v1/topology", "/api/v1/findings", "/api/v1/audit?limit=20"} {
		req, err := newAuthedRequest(http.MethodGet, base+path, sessionID, "", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("GET %s: %w", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
		}
	}
	return nil
}

// churnDraft creates a draft changeset (a harmless bridge.create on pve1,
// a fresh name each cycle so it never collides with a still-open draft),
// re-validates it, then discards it — real load on the change engine's
// validation pipeline and the changesets table (insert then delete), with
// no permanent growth (drafts don't appear in the audit log per
// docs/api.md's lifecycle-action list, which starts at apply).
func churnDraft(client *http.Client, base, sessionID, csrfToken string, n int) error {
	name := fmt.Sprintf("vmbrsoak%d", n%1000)
	createBody := []byte(fmt.Sprintf(`{"title":"soak churn %d","ops":[{"op":"bridge.create","target":"bridge:pve1:%s","params":{"mtu":1500}}]}`, n, name))
	req, err := newAuthedRequest(http.MethodPost, base+"/api/v1/changesets", sessionID, csrfToken, createBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create draft: status %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		return fmt.Errorf("decoding created draft: %w (body %s)", err, body)
	}

	valReq, err := newAuthedRequest(http.MethodPost, base+"/api/v1/changesets/"+created.ID+"/validate", sessionID, csrfToken, nil)
	if err != nil {
		return err
	}
	valResp, err := client.Do(valReq)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, valResp.Body)
	_ = valResp.Body.Close()
	if valResp.StatusCode != http.StatusOK {
		return fmt.Errorf("validate draft: status %d", valResp.StatusCode)
	}

	delReq, err := newAuthedRequest(http.MethodDelete, base+"/api/v1/changesets/"+created.ID, sessionID, csrfToken, nil)
	if err != nil {
		return err
	}
	delResp, err := client.Do(delReq)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, delResp.Body)
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK && delResp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("discard draft: status %d", delResp.StatusCode)
	}
	return nil
}

// realApplyCycle exercises the FULL lifecycle for real (create -> apply ->
// await awaiting_confirm -> confirm), then immediately does the same for a
// matching bridge.delete op — so cluster state returns to baseline after
// each cycle and the number of permanent audit/snapshot rows this soak run
// adds is exactly bounded and known (2 changesets x however many cycles
// actually ran — see planning/reports/T-607.md for the count from the
// actual run).
func realApplyCycle(client *http.Client, base, sessionID, csrfToken string, cycleN int) error {
	name := fmt.Sprintf("vmbrsoakreal%d", cycleN)
	if err := applyAndConfirm(client, base, sessionID, csrfToken,
		fmt.Sprintf(`{"title":"soak real cycle %d create","ops":[{"op":"bridge.create","target":"bridge:pve1:%s","params":{"mtu":1500,"comments":"soak"}}]}`, cycleN, name)); err != nil {
		return fmt.Errorf("create+apply %s: %w", name, err)
	}
	if err := applyAndConfirm(client, base, sessionID, csrfToken,
		fmt.Sprintf(`{"title":"soak real cycle %d delete","ops":[{"op":"bridge.delete","target":"bridge:pve1:%s","params":{}}]}`, cycleN, name)); err != nil {
		return fmt.Errorf("delete+apply %s: %w", name, err)
	}
	return nil
}

func applyAndConfirm(client *http.Client, base, sessionID, csrfToken, createBody string) error {
	req, err := newAuthedRequest(http.MethodPost, base+"/api/v1/changesets", sessionID, csrfToken, []byte(createBody))
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create: status %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		return fmt.Errorf("decoding created changeset: %w (body %s)", err, body)
	}

	applyReq, err := newAuthedRequest(http.MethodPost, base+"/api/v1/changesets/"+created.ID+"/apply", sessionID, csrfToken,
		[]byte(`{"confirmTimeoutSec":30}`))
	if err != nil {
		return err
	}
	applyResp, err := client.Do(applyReq)
	if err != nil {
		return err
	}
	applyBody, _ := io.ReadAll(applyResp.Body)
	_ = applyResp.Body.Close()
	if applyResp.StatusCode != http.StatusOK && applyResp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("apply: status %d: %s", applyResp.StatusCode, applyBody)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		getReq, err := newAuthedRequest(http.MethodGet, base+"/api/v1/changesets/"+created.ID, sessionID, "", nil)
		if err != nil {
			return err
		}
		getResp, err := client.Do(getReq)
		if err != nil {
			return err
		}
		var cs struct {
			Status string `json:"status"`
		}
		getBody, _ := io.ReadAll(getResp.Body)
		_ = getResp.Body.Close()
		_ = json.Unmarshal(getBody, &cs)
		if cs.Status == "awaiting_confirm" {
			break
		}
		if cs.Status == "committed" {
			return nil // already auto-settled somehow; nothing more to do
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("changeset %s never reached awaiting_confirm (last status %q)", created.ID, cs.Status)
		}
		time.Sleep(300 * time.Millisecond)
	}

	confirmReq, err := newAuthedRequest(http.MethodPost, base+"/api/v1/changesets/"+created.ID+"/confirm", sessionID, csrfToken, nil)
	if err != nil {
		return err
	}
	confirmResp, err := client.Do(confirmReq)
	if err != nil {
		return err
	}
	confirmBody, _ := io.ReadAll(confirmResp.Body)
	_ = confirmResp.Body.Close()
	if confirmResp.StatusCode != http.StatusOK {
		return fmt.Errorf("confirm: status %d: %s", confirmResp.StatusCode, confirmBody)
	}
	return nil
}

func sampleRSS(pid int) int64 {
	if pid == 0 {
		return -1
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb
				}
			}
		}
	}
	return -1
}

func sampleGoroutines(pprofAddr string) int {
	if pprofAddr == "" {
		return -1
	}
	resp, err := http.Get("http://" + pprofAddr + "/debug/goroutines") //nolint:gosec,noctx // soak harness, fixed loopback debug addr
	if err != nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return -1
	}
	return n
}

func sampleDBSize(dbPath string) int64 {
	if dbPath == "" {
		return -1
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return -1
	}
	total := info.Size()
	// WAL mode sidecars (docs/architecture.md §7) hold uncheckpointed data
	// that's part of the DB's real on-disk footprint until a checkpoint.
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(dbPath + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}
