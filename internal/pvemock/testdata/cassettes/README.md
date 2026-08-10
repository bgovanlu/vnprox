# Cassettes — observed PVE traffic (T-2502)

One directory per PVE release. Each file is a single request/response pair recorded from a live
PVE API by `make record`, held verbatim so it can be replayed byte-identically.

```
cassettes/
  mock-three-node-vlan/   <- recorded from internal/pvemock, NOT from Proxmox
  8.3.5/                  <- what a real recording looks like (none exists yet)
```

## Read this before you trust anything in here

**No cassette in this repository has been recorded from real Proxmox hardware yet.** The only
directory present is `mock-three-node-vlan/`, recorded from `internal/pvemock` driven by
`testdata/clusters/three-node-vlan.yaml`. It exists to exercise the machinery — recording,
refusal-on-secret, replay, drift comparison — end to end in CI, and it is named `mock-*` so that
nobody can mistake it for an observation.

A mock-recorded cassette answers "does the pipeline work". It cannot answer "what does PVE
actually send", which is the question the whole card exists for. That answer arrives with the
first directory named after a real release; see
`planning/reports/needs-hardware-validation.md`.

## Recording

```
make record PVE_URL=https://pve1.lab:8006 PVE_VERSION=8.3.5 \
            PVE_TOKEN='vnprox@pve!daemon=<uuid>' PVE_NODE=pve1 [PVE_INSECURE=1]
```

Read-only: the session issues GETs and nothing else.

Use an **API token**, not a password. A ticket-auth client's first call is `POST /access/ticket`,
whose response body is a credential, and the recorder refuses to write it — naming the field. That
refusal is the guard working.

To re-record the mock set after changing a `pvemock` handler: `make record-mock`.

## What a cassette may not contain

A response body carrying a PVE ticket, a `password` field, a private key, or anything else the
shared redactor (`internal/redact`) recognises **fails the write**. It is not redacted and written:
a cassette with a hole where a ticket used to be is no longer a description of what PVE returned,
and being that description is the only thing a cassette has over a hand-written fixture.

The same scan runs on load, so hand-editing a secret into a file here fails the test suite rather
than the review.

## What is recorded

Method, path, normalised query, status, response body, and the PVE version that produced it.

**Request headers and request bodies are never recorded, in any form.** That is what makes it
structurally impossible for a cassette to carry the `Authorization` header or the password of the
login that produced it.
