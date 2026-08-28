// SPDX-License-Identifier: Apache-2.0

package apidoc

// Operations is the hand-maintained metadata for every route vnproxd serves.
//
// THIS TABLE IS A GATE, NOT A CONVENIENCE. A route registered in internal/api
// with no entry here fails TestOpenAPI_EveryRouteIsDescribed; an entry here
// that no route serves fails the same test in the other direction. Adding a
// route therefore costs one line in this file — and that cost is the point,
// because it is what stops 200-odd routes from being described only in prose
// again.
//
// Key format is "METHOD /full/path" using chi's own path templating, which is
// also OpenAPI's; see Key.
//
// A KNOWN LIMIT, stated rather than discovered later: the gate walks the
// daemon brought up under testdata/dev.toml. A route whose service is nil in
// that configuration is not mounted, so it is neither reported missing nor
// checked for being unserved. Routes behind a disabled subsystem (the MCP
// transport, the plugin hub) are therefore outside the gate until dev.toml
// enables them. Everything the dev configuration mounts — 215 operations at
// the time of writing — is covered.
//
//nolint:gochecknoglobals // a lookup table, read-only after init
var Operations = map[string]Operation{
	// --- Unauthenticated -------------------------------------------------
	"GET /api/v1/health":       {Summary: "Report daemon liveness and per-collector health.", Tag: "system", Auth: AuthNone},
	"GET /api/v1/openapi.json": {Summary: "Serve this document, generated from the router's registered routes.", Tag: "system", Auth: AuthNone},
	"POST /api/v1/auth/login":  {Summary: "Exchange Proxmox credentials for a vnprox session.", Tag: "auth", Auth: AuthNone},

	// --- Session, general ------------------------------------------------
	"POST /api/v1/auth/logout": {Summary: "Invalidate the current session.", Tag: "auth", Auth: AuthSession},
	"GET /api/v1/auth/me":      {Summary: "Report the current principal and its capabilities.", Tag: "auth", Auth: AuthSession},
	"GET /api/v1/config":       {Summary: "Report non-secret instance configuration for the UI.", Tag: "system", Auth: AuthSession},
	"GET /api/v1/dashboard":    {Summary: "Return the dashboard summary tiles.", Tag: "dashboard", Auth: AuthSession},
	"GET /api/ws":              {Summary: "Upgrade to the topology delta WebSocket feed.", Tag: "topology", Auth: AuthSession},

	// --- Topology and inventory -------------------------------------------
	"GET /api/v1/topology":                    {Summary: "Return the cluster network graph.", Tag: "topology", Auth: AuthSession},
	"GET /api/v1/topology/diff":               {Summary: "Diff the cluster against a past point, marking what vnprox did not do.", Tag: "topology", Auth: AuthSession},
	"GET /api/v1/ports":                       {Summary: "List physical and virtual ports across the cluster.", Tag: "topology", Auth: AuthSession},
	"GET /api/v1/inventory/search":            {Summary: "Search the inventory by name, address or kind.", Tag: "inventory", Auth: AuthSession},
	"GET /api/v1/inventory/history":           {Summary: "Return what has changed one entity, and by whom.", Tag: "inventory", Auth: AuthSession},
	"GET /api/v1/nodes/{node}/interfaces/raw": {Summary: "Return one node's raw /etc/network/interfaces.", Tag: "nodes", Auth: AuthSession},
	"POST /api/v1/interfaces/lint":            {Summary: "Lint an interfaces file without staging it.", Tag: "nodes", Auth: AuthSession},

	// --- Change engine ----------------------------------------------------
	"GET /api/v1/changesets":                               {Summary: "List changesets.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets":                              {Summary: "Stage a new draft changeset.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/changesets/{id}":                          {Summary: "Return one changeset.", Tag: "changesets", Auth: AuthSession},
	"PUT /api/v1/changesets/{id}":                          {Summary: "Replace a draft changeset's operations.", Tag: "changesets", Auth: AuthSession},
	"DELETE /api/v1/changesets/{id}":                       {Summary: "Discard a changeset.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/validate":                {Summary: "Validate a changeset without applying it.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/changesets/{id}/diff":                     {Summary: "Return the config diff a changeset would produce.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/changesets/{id}/impact":                   {Summary: "Report which guests and paths a changeset disrupts.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/changesets/{id}/preview":                  {Summary: "Project the topology map as it would be with a changeset applied.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/changesets/{id}/sdn-foreign-pending":      {Summary: "List foreign SDN pending state an apply would also commit.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/sdn-foreign-pending/ack": {Summary: "Acknowledge the current foreign SDN pending state before applying.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/preflight-impact":        {Summary: "Report impact for a proposed edit before it is saved.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/apply":                   {Summary: "Apply a changeset under the commit-confirm timer.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/confirm":                 {Summary: "Confirm an applied changeset before its timer expires.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/continue":                {Summary: "Promote a staged (canary) apply past its hold to the remaining nodes.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/rollback":                {Summary: "Roll an applied changeset back to its pre-apply state.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/approve":                 {Summary: "Record an approval on a changeset.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/propose":                 {Summary: "Propose a changeset as a pull request against the spec repository.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/changesets/{id}/proposal":                 {Summary: "Return the pull request a changeset was proposed as.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/review/approve":          {Summary: "Approve a changeset in four-eyes review.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/review/reject":           {Summary: "Reject a changeset in four-eyes review.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/break-glass":             {Summary: "Record an emergency override of the two-person rule, with a written reason.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/freeze-override":         {Summary: "Record an audited override of a declared freeze-window policy rule, with a written reason.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/comments":                {Summary: "Add a review comment to a changeset.", Tag: "changesets", Auth: AuthSession},
	"DELETE /api/v1/changesets/{id}/comments/{commentId}":  {Summary: "Delete a review comment.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/schedule":                {Summary: "Schedule a changeset to apply in a maintenance window.", Tag: "changesets", Auth: AuthSession},
	"DELETE /api/v1/changesets/{id}/schedule":              {Summary: "Cancel a changeset's schedule.", Tag: "changesets", Auth: AuthSession},
	"POST /api/v1/changesets/{id}/schedule/ack":            {Summary: "Acknowledge a scheduled apply, releasing it to run.", Tag: "changesets", Auth: AuthSession},
	// T-2805: advisory locks and presence. Reads only — a lock warns, it
	// never refuses, and there is deliberately no verb that takes or drops
	// one directly.
	"GET /api/v1/locks":    {Summary: "List the advisory locks staged drafts currently hold on entities.", Tag: "changesets", Auth: AuthSession},
	"GET /api/v1/presence": {Summary: "Report who is currently viewing a changeset or entity.", Tag: "changesets", Auth: AuthSession},

	// --- Snapshots and spec ------------------------------------------------
	"GET /api/v1/snapshots":               {Summary: "List configuration snapshots.", Tag: "snapshots", Auth: AuthSession},
	"POST /api/v1/snapshots":              {Summary: "Capture a snapshot of current node configuration.", Tag: "snapshots", Auth: AuthSession},
	"GET /api/v1/snapshots/{id}":          {Summary: "Return one snapshot's contents.", Tag: "snapshots", Auth: AuthSession},
	"GET /api/v1/snapshots/diff":          {Summary: "Diff two snapshots, or a snapshot against live config.", Tag: "snapshots", Auth: AuthSession},
	"POST /api/v1/snapshots/{id}/restore": {Summary: "Stage a changeset restoring a snapshot.", Tag: "snapshots", Auth: AuthSession},
	"GET /api/v1/spec":                    {Summary: "Export the declarative network spec.", Tag: "spec", Auth: AuthSession},
	"POST /api/v1/spec/import":            {Summary: "Import a spec, staging the changeset that reconciles it.", Tag: "spec", Auth: AuthSession},
	"GET /api/v1/spec/pin":                {Summary: "Report the pinned spec version, if any.", Tag: "spec", Auth: AuthSession},
	"POST /api/v1/spec/pin":               {Summary: "Pin the spec to a version, refusing drift from it.", Tag: "spec", Auth: AuthSession},
	"DELETE /api/v1/spec/pin":             {Summary: "Remove the spec pin.", Tag: "spec", Auth: AuthSession},
	"GET /api/v1/gitsync/status":          {Summary: "Report the git spec sync's last fetch, last plan, and why its draft is open.", Tag: "spec", Auth: AuthSession},

	// --- Drift and findings -------------------------------------------------
	"GET /api/v1/drift":                      {Summary: "Report configuration drift against the last known state.", Tag: "drift", Auth: AuthSession},
	"POST /api/v1/drift/{id}/fix":            {Summary: "Stage a changeset reconciling one drift item.", Tag: "drift", Auth: AuthSession},
	"POST /api/v1/drift/{id}/restore-intent": {Summary: "Stage a changeset bringing the cluster back to what the spec declares.", Tag: "drift", Auth: AuthSession},
	"POST /api/v1/drift/{id}/adopt-reality":  {Summary: "Propose a spec commit describing the cluster as it is.", Tag: "drift", Auth: AuthSession},
	"GET /api/v1/drift/{id}/adoption":        {Summary: "Return the pull request this drift finding was adopted as.", Tag: "drift", Auth: AuthSession},

	"GET /api/v1/digest/schedule":                        {Summary: "Return the scheduled-digest cadence, recipients, and last run.", Tag: "digest", Auth: AuthSession},
	"PUT /api/v1/digest/schedule":                        {Summary: "Change the scheduled-digest cadence, recipients, or enablement.", Tag: "digest", Auth: AuthSession},
	"GET /api/v1/findings":                               {Summary: "List open findings, optionally filtered by acknowledgement.", Tag: "findings", Auth: AuthSession},
	"POST /api/v1/findings/{id}/fix":                     {Summary: "Stage a changeset fixing one finding.", Tag: "findings", Auth: AuthSession},
	"POST /api/v1/findings/fix":                          {Summary: "Stage one changeset fixing several findings together.", Tag: "findings", Auth: AuthSession},
	"POST /api/v1/findings/{id}/ack":                     {Summary: "Acknowledge a finding, with a reason and optional expiry.", Tag: "findings", Auth: AuthSession},
	"POST /api/v1/findings/{id}/runbooks/{name}/prepare": {Summary: "Run a runbook's read-checks and stage its remediation changeset for review. Prepares only; never applies.", Tag: "findings", Auth: AuthSession},
	"DELETE /api/v1/findings/{id}/ack":                   {Summary: "Withdraw a finding's acknowledgement.", Tag: "findings", Auth: AuthSession},

	// --- Audit, history, doctor ----------------------------------------------
	"GET /api/v1/audit":          {Summary: "Read the audit log.", Tag: "audit", Auth: AuthSession},
	"GET /api/v1/history/events": {Summary: "Return the unified event timeline.", Tag: "history", Auth: AuthSession},
	"GET /api/v1/doctor/live":    {Summary: "Run the self-checks that need the daemon's own credentials.", Tag: "doctor", Auth: AuthSession},

	// --- Incidents (T-2804) ----------------------------------------------------
	"GET /api/v1/incidents":                   {Summary: "List incidents.", Tag: "incidents", Auth: AuthSession},
	"POST /api/v1/incidents":                  {Summary: "Open an incident over a window, live or retroactive.", Tag: "incidents", Auth: AuthSession},
	"GET /api/v1/incidents/{id}":              {Summary: "Read one incident and its annotations.", Tag: "incidents", Auth: AuthSession},
	"GET /api/v1/incidents/{id}/timeline":     {Summary: "Assemble one incident's merged timeline.", Tag: "incidents", Auth: AuthSession},
	"GET /api/v1/incidents/{id}/postmortem":   {Summary: "Render one incident's timeline as a filed postmortem document (format=md|html). Distinct from the support-bundle export.", Tag: "incidents", Auth: AuthSession},
	"POST /api/v1/incidents/{id}/annotations": {Summary: "Add an operator observation to the timeline.", Tag: "incidents", Auth: AuthSession},
	"POST /api/v1/incidents/{id}/close":       {Summary: "Close an incident, freezing its window.", Tag: "incidents", Auth: AuthSession},
	"POST /api/v1/incidents/{id}/reopen":      {Summary: "Reopen a closed incident.", Tag: "incidents", Auth: AuthSession},
	"GET /api/v1/incidents/{id}/export":       {Summary: "Download the incident artifact: the timeline plus a support bundle.", Tag: "incidents", Auth: AuthSession},

	// --- Metrics ---------------------------------------------------------------
	"GET /api/v1/metrics":         {Summary: "Prometheus exposition of vnprox and cluster metrics.", Tag: "metrics", Auth: AuthBearer},
	"GET /api/v1/metrics/live":    {Summary: "Return current interface counters and rates.", Tag: "metrics", Auth: AuthSession},
	"GET /api/v1/metrics/history": {Summary: "Return stored metric samples over a time range.", Tag: "metrics", Auth: AuthSession},

	// --- Alerting and webhooks ---------------------------------------------------
	"GET /api/v1/alert-rules":            {Summary: "List alert rules.", Tag: "alerts", Auth: AuthSession},
	"POST /api/v1/alert-rules":           {Summary: "Create an alert rule.", Tag: "alerts", Auth: AuthSession},
	"GET /api/v1/alert-rules/{id}":       {Summary: "Return one alert rule.", Tag: "alerts", Auth: AuthSession},
	"PUT /api/v1/alert-rules/{id}":       {Summary: "Update an alert rule.", Tag: "alerts", Auth: AuthSession},
	"DELETE /api/v1/alert-rules/{id}":    {Summary: "Delete an alert rule.", Tag: "alerts", Auth: AuthSession},
	"POST /api/v1/alert-rules/{id}/test": {Summary: "Send a test delivery for an alert rule.", Tag: "alerts", Auth: AuthSession},
	"GET /api/v1/alert-deliveries":       {Summary: "List alert delivery attempts and their outcomes.", Tag: "alerts", Auth: AuthSession},
	"GET /api/v1/webhooks":               {Summary: "List outbound webhooks.", Tag: "webhooks", Auth: AuthSession},
	"POST /api/v1/webhooks":              {Summary: "Register an outbound webhook.", Tag: "webhooks", Auth: AuthSession},
	"DELETE /api/v1/webhooks/{id}":       {Summary: "Delete an outbound webhook.", Tag: "webhooks", Auth: AuthSession},

	// --- Web push (T-2005) -------------------------------------------------------
	"GET /api/v1/push/vapid-public-key":      {Summary: "Return this daemon's VAPID public key for PushManager.subscribe().", Tag: "push", Auth: AuthSession},
	"GET /api/v1/push/subscriptions":         {Summary: "List the caller's own push subscriptions.", Tag: "push", Auth: AuthSession},
	"POST /api/v1/push/subscriptions":        {Summary: "Register a push subscription, opted into a subset of critical/awaitingConfirm/drift.", Tag: "push", Auth: AuthSession},
	"DELETE /api/v1/push/subscriptions/{id}": {Summary: "Revoke one of the caller's own push subscriptions.", Tag: "push", Auth: AuthSession},

	// --- Tokens and embedding ---------------------------------------------------
	"GET /api/v1/tokens":         {Summary: "List API tokens.", Tag: "tokens", Auth: AuthSession},
	"POST /api/v1/tokens":        {Summary: "Mint an API token; the secret is returned once.", Tag: "tokens", Auth: AuthSession},
	"DELETE /api/v1/tokens/{id}": {Summary: "Revoke an API token.", Tag: "tokens", Auth: AuthSession},
	"POST /api/v1/embed/tokens":  {Summary: "Mint a scoped, read-only embed-view token.", Tag: "tokens", Auth: AuthSession},

	// --- Layout and annotations ---------------------------------------------------
	"GET /api/v1/layouts":             {Summary: "List saved topology layouts.", Tag: "layouts", Auth: AuthSession},
	"GET /api/v1/layouts/{name}":      {Summary: "Return one saved layout.", Tag: "layouts", Auth: AuthSession},
	"PUT /api/v1/layouts/{name}":      {Summary: "Save a topology layout.", Tag: "layouts", Auth: AuthSession},
	"DELETE /api/v1/layouts/{name}":   {Summary: "Delete a saved layout.", Tag: "layouts", Auth: AuthSession},
	"GET /api/v1/annotations":         {Summary: "List map annotations.", Tag: "annotations", Auth: AuthSession},
	"POST /api/v1/annotations":        {Summary: "Create a map annotation.", Tag: "annotations", Auth: AuthSession},
	"DELETE /api/v1/annotations/{id}": {Summary: "Delete a map annotation.", Tag: "annotations", Auth: AuthSession},
	"GET /api/v1/map-regions":         {Summary: "List labelled canvas regions.", Tag: "annotations", Auth: AuthSession},
	"POST /api/v1/map-regions":        {Summary: "Draw a labelled canvas region.", Tag: "annotations", Auth: AuthSession},
	"DELETE /api/v1/map-regions/{id}": {Summary: "Delete a labelled canvas region.", Tag: "annotations", Auth: AuthSession},

	// --- Federation ------------------------------------------------------------
	"GET /api/v1/federation/clusters":               {Summary: "List federated clusters.", Tag: "federation", Auth: AuthSession},
	"POST /api/v1/federation/clusters":              {Summary: "Register a federated cluster.", Tag: "federation", Auth: AuthSession},
	"GET /api/v1/federation/clusters/{id}":          {Summary: "Return one federated cluster.", Tag: "federation", Auth: AuthSession},
	"PUT /api/v1/federation/clusters/{id}":          {Summary: "Update a federated cluster's connection details.", Tag: "federation", Auth: AuthSession},
	"DELETE /api/v1/federation/clusters/{id}":       {Summary: "Deregister a federated cluster.", Tag: "federation", Auth: AuthSession},
	"GET /api/v1/federation/topology":               {Summary: "Return the aggregated multi-cluster topology.", Tag: "federation", Auth: AuthSession},
	"GET /api/v1/federation/topology/clusters/{id}": {Summary: "Return one federated cluster's topology.", Tag: "federation", Auth: AuthSession},
	"GET /api/v1/federation/search":                 {Summary: "Search inventory across every federated cluster.", Tag: "federation", Auth: AuthSession},
	"GET /api/v1/federation/ipam/conflicts":         {Summary: "Report overlapping subnets across federated clusters.", Tag: "federation", Auth: AuthSession},

	// --- SDN, IPAM, edge -----------------------------------------------------------
	"GET /api/v1/sdn":                           {Summary: "Return the SDN zone, vnet and subnet inventory.", Tag: "sdn", Auth: AuthSession},
	"GET /api/v1/sdn/dns":                       {Summary: "Report SDN DNS integration state.", Tag: "sdn", Auth: AuthSession},
	"GET /api/v1/sdn/dhcp":                      {Summary: "Report SDN DHCP configuration and leases.", Tag: "sdn", Auth: AuthSession},
	"GET /api/v1/sdn/evpn/status":               {Summary: "Report EVPN control-plane status per node.", Tag: "sdn", Auth: AuthSession},
	"GET /api/v1/ipam/subnets":                  {Summary: "List known subnets and their utilisation.", Tag: "ipam", Auth: AuthSession},
	"GET /api/v1/ipam/external-subnets":         {Summary: "List subnets imported from an external IPAM.", Tag: "ipam", Auth: AuthSession},
	"POST /api/v1/ipam/external-subnets":        {Summary: "Register an external IPAM subnet source.", Tag: "ipam", Auth: AuthSession},
	"GET /api/v1/ipam/external-subnets/{id}":    {Summary: "Return one external IPAM subnet.", Tag: "ipam", Auth: AuthSession},
	"PUT /api/v1/ipam/external-subnets/{id}":    {Summary: "Update an external IPAM subnet.", Tag: "ipam", Auth: AuthSession},
	"DELETE /api/v1/ipam/external-subnets/{id}": {Summary: "Remove an external IPAM subnet.", Tag: "ipam", Auth: AuthSession},
	"POST /api/v1/ipam/external-sync/preview":   {Summary: "Preview what an external IPAM sync would change.", Tag: "ipam", Auth: AuthSession},
	"POST /api/v1/ipam/external-sync/apply":     {Summary: "Stage the changeset an external IPAM sync implies.", Tag: "ipam", Auth: AuthSession},
	"GET /api/v1/edge/routes":                   {Summary: "Return edge routing state.", Tag: "edge", Auth: AuthSession},
	"GET /api/v1/edge/nat":                      {Summary: "Return edge NAT rules.", Tag: "edge", Auth: AuthSession},
	"GET /api/v1/ipv6/segments":                 {Summary: "Report IPv6 prefix delegation and segments.", Tag: "ipv6", Auth: AuthSession},

	// --- Ingress -------------------------------------------------------------------
	"GET /api/v1/ingress/targets":         {Summary: "List ingress targets.", Tag: "ingress", Auth: AuthSession},
	"POST /api/v1/ingress/targets":        {Summary: "Register an ingress target.", Tag: "ingress", Auth: AuthSession},
	"DELETE /api/v1/ingress/targets/{id}": {Summary: "Remove an ingress target.", Tag: "ingress", Auth: AuthSession},
	"GET /api/v1/ingress/status":          {Summary: "Report reachability of each ingress target.", Tag: "ingress", Auth: AuthSession},

	// --- Firewall and microsegmentation ------------------------------------------------
	"GET /api/v1/firewall/rulesets": {Summary: "Return firewall rulesets by scope.", Tag: "firewall", Auth: AuthSession},
	"GET /api/v1/firewall/objects":  {Summary: "Return firewall IP sets and aliases.", Tag: "firewall", Auth: AuthSession},
	"GET /api/v1/firewall/effects":  {Summary: "Report which rules actually affect a given flow.", Tag: "firewall", Auth: AuthSession},
	"POST /api/v1/microseg/dry-run": {Summary: "Evaluate a microsegmentation policy without staging it.", Tag: "microseg", Auth: AuthSession},
	"POST /api/v1/microseg/propose": {Summary: "Propose microsegmentation rules from observed flows.", Tag: "microseg", Auth: AuthSession},

	// --- Diagnostics ---------------------------------------------------------------------
	"POST /api/v1/diagnose":                   {Summary: "Run the guided connectivity diagnosis.", Tag: "diagnose", Auth: AuthSession},
	"POST /api/v1/simulate/path":              {Summary: "Simulate a packet's path through the topology.", Tag: "simulate", Auth: AuthSession},
	"POST /api/v1/simulate/verify":            {Summary: "Verify a simulated path against a live probe.", Tag: "simulate", Auth: AuthSession},
	"GET /api/v1/simulate/verify/eligibility": {Summary: "Report whether live probe verification is available.", Tag: "simulate", Auth: AuthSession},
	"GET /api/v1/conntrack":                   {Summary: "Return conntrack entries across the cluster.", Tag: "conntrack", Auth: AuthSession},
	"GET /api/v1/mdb":                         {Summary: "Return bridge multicast forwarding-database entries and snooping config across the cluster.", Tag: "mdb", Auth: AuthSession},
	"GET /api/v1/firewall/compiled":           {Summary: "Return the nftables ruleset the node has actually installed, cross-linked to the vnprox rules that produced it where attribution is reliable.", Tag: "firewall", Auth: AuthSession},
	"GET /api/v1/dashboard/tiles":             {Summary: "Return dashboard tiles contributed by enabled plugins, for composition alongside the built-in tiles.", Tag: "dashboard", Auth: AuthSession},
	"GET /api/v1/route/nodes":                 {Summary: "List nodes a routing snapshot or lookup can currently target.", Tag: "route", Auth: AuthSession},
	"GET /api/v1/route/snapshot":              {Summary: "Return a node's kernel FIB, policy rules, and FRR RIB.", Tag: "route", Auth: AuthSession},
	"GET /api/v1/route/lookup":                {Summary: "Report which path a destination address would take from a node.", Tag: "route", Auth: AuthSession},
	"GET /api/v1/flows":                       {Summary: "Return observed network flows.", Tag: "flows", Auth: AuthSession},
	"GET /api/v1/neighbors/history":           {Summary: "Return IP<->MAC binding transition history across the cluster.", Tag: "neighbors", Auth: AuthSession},
	"GET /api/v1/fdb":                         {Summary: "Return bridge forwarding-database entries.", Tag: "fdb", Auth: AuthSession},
	"GET /api/v1/lldp":                        {Summary: "Return LLDP neighbours per node.", Tag: "lldp", Auth: AuthSession},
	"POST /api/v1/lldp/install":               {Summary: "Install the LLDP daemon on selected nodes.", Tag: "lldp", Auth: AuthSession},
	"GET /api/v1/lldp/vlan-check":             {Summary: "Compare switch-side VLANs against node expectations.", Tag: "lldp", Auth: AuthSession},
	"GET /api/v1/mtuprobe/results":            {Summary: "Return path-MTU probe results.", Tag: "mtuprobe", Auth: AuthSession},
	"GET /api/v1/latmesh/heatmap":             {Summary: "Return the node-to-node latency heatmap.", Tag: "latmesh", Auth: AuthSession},
	"GET /api/v1/latmesh/history":             {Summary: "Return latency history for a node pair.", Tag: "latmesh", Auth: AuthSession},
	"GET /api/v1/failsim/spof-score":          {Summary: "Score single points of failure in the current topology.", Tag: "failsim", Auth: AuthSession},
	"POST /api/v1/migration/preflight":        {Summary: "Check whether a guest migration is network-safe.", Tag: "migration", Auth: AuthSession},
	"POST /api/v1/collectors/refresh":         {Summary: "Re-run a collector poll now.", Tag: "topology", Auth: AuthSession},
	"POST /api/v1/services/start":             {Summary: "Start a watched SDN service on a node.", Tag: "topology", Auth: AuthSession},

	// --- Captures ----------------------------------------------------------------------------
	"GET /api/v1/captures":               {Summary: "List packet captures.", Tag: "captures", Auth: AuthSession},
	"POST /api/v1/captures":              {Summary: "Start a packet capture.", Tag: "captures", Auth: AuthSession},
	"GET /api/v1/captures/{id}":          {Summary: "Return one capture's status.", Tag: "captures", Auth: AuthSession},
	"POST /api/v1/captures/{id}/stop":    {Summary: "Stop a running capture.", Tag: "captures", Auth: AuthSession},
	"GET /api/v1/captures/{id}/download": {Summary: "Download a capture's pcap file.", Tag: "captures", Auth: AuthSession},

	// --- Guests, QoS, WAN ----------------------------------------------------------------------
	"GET /api/v1/guests/{ref}/interior":        {Summary: "Return one guest's interior network view.", Tag: "guests", Auth: AuthSession},
	"GET /api/v1/guests/{ref}/interior-toggle": {Summary: "Report whether interior inspection is enabled for a guest.", Tag: "guests", Auth: AuthSession},
	"PUT /api/v1/guests/{ref}/interior-toggle": {Summary: "Enable or disable interior inspection for a guest.", Tag: "guests", Auth: AuthSession},
	"GET /api/v1/qos/shapes":                   {Summary: "Return traffic-shaping configuration per interface.", Tag: "qos", Auth: AuthSession},
	"GET /api/v1/wan/status":                   {Summary: "Report WAN uplink health.", Tag: "wan", Auth: AuthSession},
	"GET /api/v1/wan/targets":                  {Summary: "List WAN probe targets.", Tag: "wan", Auth: AuthSession},
	"PUT /api/v1/wan/targets":                  {Summary: "Replace the WAN probe target list.", Tag: "wan", Auth: AuthSession},

	// --- Safety, posture, capacity ------------------------------------------------------------------
	"GET /api/v1/policies":                     {Summary: "Read the cluster's declarative policy rule set and per-rule statistics.", Tag: "policy", Auth: AuthSession},
	"PUT /api/v1/policies":                     {Summary: "Replace the cluster's declarative policy rule set.", Tag: "policy", Auth: AuthSession},
	"POST /api/v1/policies/test":               {Summary: "Evaluate a policy rule set against a changeset without staging it.", Tag: "policy", Auth: AuthSession},
	"GET /api/v1/calendar":                     {Summary: "List declared freeze windows alongside pending scheduled changesets and node maintenance windows.", Tag: "policy", Auth: AuthSession},
	"GET /api/v1/maintenance-windows":          {Summary: "List every declared node maintenance window.", Tag: "policy", Auth: AuthSession},
	"POST /api/v1/maintenance-windows":         {Summary: "Declare a node maintenance window that suppresses its findings/alerts.", Tag: "policy", Auth: AuthSession},
	"DELETE /api/v1/maintenance-windows/{id}":  {Summary: "End a declared node maintenance window early.", Tag: "policy", Auth: AuthSession},
	"GET /api/v1/nodes/{node}/maintenance":     {Summary: "Report whether a node is currently inside a declared maintenance window.", Tag: "policy", Auth: AuthSession},
	"GET /api/v1/protected-interfaces":         {Summary: "List interfaces protected from modification.", Tag: "protected", Auth: AuthSession},
	"PUT /api/v1/protected-interfaces":         {Summary: "Replace the protected-interface list.", Tag: "protected", Auth: AuthSession},
	"GET /api/v1/protected-interfaces/status":  {Summary: "Report whether protection is currently effective.", Tag: "protected", Auth: AuthSession},
	"GET /api/v1/protected-interfaces/suggest": {Summary: "Suggest interfaces that should be protected.", Tag: "protected", Auth: AuthSession},
	"GET /api/v1/posture":                      {Summary: "Return the current security posture assessment.", Tag: "posture", Auth: AuthSession},
	"GET /api/v1/posture/history":              {Summary: "Return posture scores over time.", Tag: "posture", Auth: AuthSession},
	"GET /api/v1/export/posture":               {Summary: "Export the posture report.", Tag: "export", Auth: AuthSession},
	"GET /api/v1/compliance":                   {Summary: "List the installed compliance profiles. Not a certification: a profile maps controls onto evidence vnprox already produces.", Tag: "compliance", Auth: AuthSession},
	"GET /api/v1/compliance/{profile}":         {Summary: "Report one profile's controls with the evidence behind each. A control with no mapped evidence reports `unmapped`, never `pass`.", Tag: "compliance", Auth: AuthSession},
	"GET /api/v1/export/compliance/{profile}":  {Summary: "Export the compliance report as a timestamped Markdown, HTML or JSON document.", Tag: "export", Auth: AuthSession},
	"GET /api/v1/export/doc":                   {Summary: "Export network documentation.", Tag: "export", Auth: AuthSession},
	"GET /api/v1/capacity/export":              {Summary: "Export the capacity report.", Tag: "capacity", Auth: AuthSession},
	"GET /api/v1/certs":                        {Summary: "Return the TLS certificate inventory and expiry.", Tag: "certs", Auth: AuthSession},
	"GET /api/v1/ceph/status":                  {Summary: "Report Ceph network health.", Tag: "ceph", Auth: AuthSession},
	"GET /api/v1/pbs":                          {Summary: "Report Proxmox Backup Server reachability.", Tag: "pbs", Auth: AuthSession},

	// --- Blueprints and plugins --------------------------------------------------------------------------
	"GET /api/v1/blueprints":                         {Summary: "List blueprints.", Tag: "blueprints", Auth: AuthSession},
	"POST /api/v1/blueprints":                        {Summary: "Create a blueprint.", Tag: "blueprints", Auth: AuthSession},
	"GET /api/v1/blueprints/{id}":                    {Summary: "Return one blueprint.", Tag: "blueprints", Auth: AuthSession},
	"DELETE /api/v1/blueprints/{id}":                 {Summary: "Delete a blueprint.", Tag: "blueprints", Auth: AuthSession},
	"POST /api/v1/blueprints/capture":                {Summary: "Capture current configuration as a blueprint.", Tag: "blueprints", Auth: AuthSession},
	"POST /api/v1/blueprints/import":                 {Summary: "Import a signed blueprint bundle.", Tag: "blueprints", Auth: AuthSession},
	"GET /api/v1/blueprints/{id}/bundle":             {Summary: "Download a blueprint as a signed bundle.", Tag: "blueprints", Auth: AuthSession},
	"POST /api/v1/blueprints/{id}/instantiate":       {Summary: "Stage the changeset a blueprint implies.", Tag: "blueprints", Auth: AuthSession},
	"GET /api/v1/blueprints/{id}/suggest":            {Summary: "Suggest parameter values for a blueprint.", Tag: "blueprints", Auth: AuthSession},
	"GET /api/v1/blueprints/signing-key":             {Summary: "Return this instance's blueprint signing public key.", Tag: "blueprints", Auth: AuthSession},
	"GET /api/v1/blueprint-signers":                  {Summary: "List trusted blueprint signers.", Tag: "blueprints", Auth: AuthSession},
	"POST /api/v1/blueprint-signers":                 {Summary: "Trust a blueprint signer.", Tag: "blueprints", Auth: AuthSession},
	"DELETE /api/v1/blueprint-signers/{fingerprint}": {Summary: "Stop trusting a blueprint signer.", Tag: "blueprints", Auth: AuthSession},
	"GET /api/v1/plugins":                            {Summary: "List installed plugins.", Tag: "plugins", Auth: AuthSession},
	"DELETE /api/v1/plugins/{id}":                    {Summary: "Uninstall a plugin.", Tag: "plugins", Auth: AuthSession},
	"POST /api/v1/plugins/{id}/enable":               {Summary: "Enable an installed plugin.", Tag: "plugins", Auth: AuthSession},
	"POST /api/v1/plugins/{id}/disable":              {Summary: "Disable an installed plugin.", Tag: "plugins", Auth: AuthSession},

	// --- Kubernetes and WireGuard ---------------------------------------------------------------------------
	"GET /api/v1/k8s/clusters":                       {Summary: "List connected Kubernetes clusters.", Tag: "k8s", Auth: AuthSession},
	"POST /api/v1/k8s/clusters":                      {Summary: "Connect a Kubernetes cluster.", Tag: "k8s", Auth: AuthSession},
	"DELETE /api/v1/k8s/clusters/{id}":               {Summary: "Disconnect a Kubernetes cluster.", Tag: "k8s", Auth: AuthSession},
	"GET /api/v1/k8s/{clusterId}/overlay":            {Summary: "Return a Kubernetes cluster's network overlay.", Tag: "k8s", Auth: AuthSession},
	"GET /api/v1/wireguard/tunnels":                  {Summary: "List WireGuard tunnels.", Tag: "wireguard", Auth: AuthSession},
	"GET /api/v1/wireguard/tunnels/{id}/pubkey":      {Summary: "Return a tunnel's public key.", Tag: "wireguard", Auth: AuthSession},
	"GET /api/v1/wireguard/tunnels/{id}/peer-config": {Summary: "Return the peer-side configuration for a tunnel.", Tag: "wireguard", Auth: AuthSession},

	// --- Tenants ------------------------------------------------------------------------------------------------
	"GET /api/v1/tenants":                            {Summary: "List tenants.", Tag: "tenants", Auth: AuthSession},
	"POST /api/v1/tenants":                           {Summary: "Create a tenant.", Tag: "tenants", Auth: AuthSession},
	"GET /api/v1/tenants/{id}":                       {Summary: "Return one tenant.", Tag: "tenants", Auth: AuthSession},
	"DELETE /api/v1/tenants/{id}":                    {Summary: "Delete a tenant.", Tag: "tenants", Auth: AuthSession},
	"PUT /api/v1/tenants/{id}/members":               {Summary: "Replace a tenant's membership.", Tag: "tenants", Auth: AuthSession},
	"DELETE /api/v1/tenants/{id}/members/{identity}": {Summary: "Remove one member from a tenant.", Tag: "tenants", Auth: AuthSession},
	"PUT /api/v1/tenants/{id}/scopes":                {Summary: "Replace a tenant's resource scopes.", Tag: "tenants", Auth: AuthSession},
	"DELETE /api/v1/tenants/{id}/scopes":             {Summary: "Clear a tenant's resource scopes.", Tag: "tenants", Auth: AuthSession},

	// --- Peer API (cluster-internal; HMAC, never a session) ---------------------------------
	"GET /api/peer/health":                  {Summary: "Peer liveness probe.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/version":                 {Summary: "Report the peer's vnprox version.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/audit":                   {Summary: "Return the peer's audit entries.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/snapshots":               {Summary: "Return the peer's snapshots.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/flows":                   {Summary: "Return flows observed by the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/firewall/log":            {Summary: "Return the peer's firewall log.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/ha/replicate":           {Summary: "Replicate HA state to the peer.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/capture/start":          {Summary: "Start a capture on the peer.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/capture/stop":           {Summary: "Stop a capture on the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/capture/status":          {Summary: "Report capture status on the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/capture/download":        {Summary: "Download a capture from the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/interfaces":         {Summary: "Return the peer's /etc/network/interfaces.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/host/stage-interfaces":  {Summary: "Stage an interfaces file on the peer.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/host/discard-staged":    {Summary: "Discard the peer's staged interfaces file.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/host/ifreload":          {Summary: "Reload networking on the peer.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/host/restore":           {Summary: "Restore the peer's previous interfaces file.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/links":              {Summary: "Return the peer's link states.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/stats":              {Summary: "Return the peer's interface counters.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/neighbors":          {Summary: "Return the peer's neighbour table.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/neighbors/history":  {Summary: "Return the peer's IP<->MAC binding transition history.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/fdb":                {Summary: "Return the peer's bridge forwarding database.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/lldp":               {Summary: "Return the peer's LLDP neighbours.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/host/lldp/install":      {Summary: "Install the LLDP daemon on the peer.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/host/service/start":     {Summary: "Start a watched SDN service on the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/services":           {Summary: "Report networking service states on the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/conntrack":          {Summary: "Return the peer's conntrack entries.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/mdb":                {Summary: "Return the peer's bridge multicast forwarding database.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/nftables":           {Summary: "Return the peer's compiled nftables ruleset.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/route/fib-v4":       {Summary: "Return the peer's IPv4 kernel routing table.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/route/fib-v6":       {Summary: "Return the peer's IPv6 kernel routing table.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/route/rules-v4":     {Summary: "Return the peer's IPv4 policy-routing rules.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/route/rules-v6":     {Summary: "Return the peer's IPv6 policy-routing rules.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/route/frr-rib-v4":   {Summary: "Return the peer's IPv4 FRR RIB.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/route/frr-rib-v6":   {Summary: "Return the peer's IPv6 FRR RIB.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/dhcp-leases":        {Summary: "Return DHCP leases known to the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/ipv6-ra":            {Summary: "Return IPv6 router advertisements seen by the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/container-interior": {Summary: "Return a container's interior view from the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/container-ping":     {Summary: "Run a ping from inside a container on the peer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/frr/bgp-summary":    {Summary: "Return the peer's FRR BGP summary.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/host/frr/evpn-vni":       {Summary: "Return the peer's FRR EVPN VNI table.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/timer/arm":              {Summary: "Arm the peer's commit-confirm rollback timer.", Tag: "peer", Auth: AuthPeer},
	"POST /api/peer/timer/cancel":           {Summary: "Cancel the peer's rollback timer.", Tag: "peer", Auth: AuthPeer},
	"GET /api/peer/timer/status":            {Summary: "Report the peer's rollback timer state.", Tag: "peer", Auth: AuthPeer},
}
