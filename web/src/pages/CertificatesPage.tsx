// SPDX-License-Identifier: Apache-2.0

// The real implementation lives in src/certs/ (the certificate inventory
// feature module) alongside its own tests — this file only wires it to the
// routed /settings/certificates path App.tsx expects, per the existing
// per-route-file layout (see pages/FederationClustersPage.tsx).
export { CertificatesPage } from "../certs/CertificatesPage";
