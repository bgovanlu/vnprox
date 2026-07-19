// Explanatory copy for the "connect two clusters" wizard (T-1402) — kept
// separate from the component like sdn/wizards/strings.ts's own precedent,
// written for a non-networking-expert reader (docs/features/sdn.md §2's
// bar, reused here since this wizard is built on the same WizardShell).
export const wgWizardStrings = {
  title: "Connect two clusters — guided setup",
  intro:
    "Builds a site-to-site WireGuard tunnel from one of this cluster's nodes to another endpoint, plus the firewall rule it needs to work — as one reviewable changeset. Nothing is created until you finish the last step.",
  federationNote:
    "vnprox doesn't yet automate exchanging keys between two vnprox-managed clusters (that lands with federation, a later phase). For now, paste in the far side's own WireGuard public key — either a genuinely external system's key, or another vnprox cluster/node's own key, copied from its Public key page.",
  steps: {
    source: "This side",
    peer: "Other side",
    firewall: "Firewall",
    review: "Review",
  },
  sourceHelp: {
    node: "Which of this cluster's nodes hosts the tunnel.",
    ifName: "The on-node WireGuard interface name, e.g. wg0.",
    listenPort: "UDP port this side listens on for the tunnel.",
    carrier: "Optional — the underlying interface (e.g. vmbr0) the tunnel's traffic actually rides on. Leave blank if unsure.",
    localAddress: "This tunnel's own address on the private tunnel network, e.g. 10.10.0.1/24.",
    mtu: "Optional — leave blank to use the interface default.",
  },
  peerHelp: {
    publicKey: "The far side's WireGuard public key (44 characters, ends with '=').",
    endpoint: "The far side's reachable address, host:port — leave blank if it has no fixed address (it will dial in instead).",
    allowedIps: "Comma-separated addresses/ranges reachable through this tunnel, e.g. 10.10.0.2/32.",
    presharedKey: "Optional extra symmetric key for post-quantum resistance — leave blank if the far side doesn't have one.",
    keepalive: "Optional — send a keepalive every N seconds (useful behind NAT). Leave blank to disable.",
  },
  firewallHelp: {
    source: "Restrict the allowed source to the peer's own address for a tighter rule — leave blank to allow from anywhere (appropriate for a peer with no fixed endpoint).",
  },
  common: {
    cancelButton: "Cancel",
    backButton: "Back",
    nextButton: "Next",
    finishButton: "Create draft",
    draftNotice: "This creates a draft changeset — nothing is applied to your cluster until you review and apply it.",
    previewEmpty: "Fill in this side and the peer's public key to see a preview of the tunnel.",
  },
};
