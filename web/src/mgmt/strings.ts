// Every explanatory string T-703's management-redundancy wizard and its
// apply-side acknowledgement show a user, in one file for copy review
// (T-403's non-expert bar, carried into this task's card). Nothing here
// assumes the reader knows what a bond, LACP, or VLAN sub-interface is —
// each string defines the jargon in plain language the first time it uses
// it. Keep this the single place mgmt-wizard copy is added or edited.

export const mgmtStrings = {
  launch: {
    // The affordance label on the finding / inspector / New menu.
    button: "Make management path redundant…",
    fromFinding: "Fix: make this node's management path redundant",
  },

  picker: {
    title: "Protect this node's management connection",
    description:
      "Right now, if one cable or network card fails, you could lose your ability to manage this node. These guided options add a second path so a single failure no longer cuts you off. Nothing is applied until you review it — you'll get a draft to check first.",
    alreadyRedundant:
      "Good news: this node's management path already survives a single network-card failure. You can still add another card below for extra headroom, but nothing here is required.",
    noCarrier:
      "vnprox couldn't work out which interface carries this node's management address yet — check the Management path section in the inspector first.",
  },

  flowA: {
    card: "Bond the management uplink",
    blurb:
      "Combine the single network card your management address uses today with a second card, so traffic keeps flowing if either one fails.",
    intro:
      "Your management address currently rides on one network card. This bundles that card together with a second one into a 'bond' — a pair that acts as one connection but keeps working if either card (or its cable) fails. Your management address doesn't move and doesn't change.",
    pickCandidate: "Second network card to add",
    pickCandidateHelp:
      "Pick another card on this node to pair with the current one. A card that shows a live link and faces the same switch is the safest choice.",
    noCarrierWarn:
      "Warning: this card shows no live link right now. Bonding to a card that isn't plugged in gives you no real redundancy until it is.",
    differentSwitchWarn:
      "Heads up: this card's neighbour looks like a different switch than the current uplink's. Unless those two switches are set up as a pair (MLAG/stacking), a bond across them may not behave as expected — active-backup mode is the safe choice here.",
    modeLabel: "How the two cards share the load",
    modeActiveBackup: "Active / standby (safe default)",
    modeActiveBackupHelp:
      "One card carries everything; the other takes over instantly if it fails. This needs no special switch configuration and is the safe choice when you're not sure how your switch is set up.",
    modeLacp: "LACP (802.3ad) — both cards active",
    modeLacpHelp:
      "Both cards carry traffic at once for more throughput. This ONLY works if your switch has been configured for LACP on these two ports first — turn this on only if you know that's already done, otherwise the link won't come up.",
    bondNameLabel: "Name for the new bond",
    bondNameHelp: "A name for the bundled pair, e.g. bond0. It just needs to be unique on this node.",
  },

  flowB: {
    card: "Add a card to the management bond",
    blurb: "This node's management already uses a bond — add or swap in another card for more resilience.",
    intro:
      "This node's management already rides on a bond (a bundle of network cards acting as one). You can add another card to the bundle, or swap one out for a different card. Your management address is untouched.",
    modeLabel: "What to do",
    addLabel: "Add a card (keep the existing ones)",
    replaceLabel: "Replace a card",
    pickAdd: "Card to add",
    pickReplaceOut: "Card to remove",
    pickReplaceIn: "Card to add in its place",
  },

  flowC: {
    card: "Move management to a dedicated VLAN interface",
    blurb:
      "Put this node's management address on its own tagged VLAN interface — the tidy, production-grade layout — carrying the exact same address.",
    intro:
      "This creates a dedicated VLAN interface for management and moves this node's existing management address (and its default route) onto it — the same address, just on a purpose-built interface instead of sharing a general-purpose bridge. The address value never changes, so you never lose reachability by construction.",
    vidLabel: "Management VLAN ID",
    vidHelp:
      "The VLAN number your switch uses for management traffic (1–4094). Your switch must already tag this VLAN on the uplink — vnprox does not configure the switch.",
    carriesAddress: "This new interface will carry",
    fromCarrier: "moved off",
  },

  ack: {
    heading: "This change touches how you reach this node",
    body:
      "This changeset alters the network path your management connection uses. If something goes wrong, vnprox's safety net automatically undoes the change unless you confirm within the countdown window after it applies — so even a mistake here can't lock you out for more than that window. To continue, type this node's name to acknowledge you understand.",
    confirmWindowNote:
      "The confirm window for a management-path change is at least 180 seconds and can't be shortened — that's your time to check you still have a connection before the change becomes permanent.",
    typePrompt: (node: string) => `Type “${node}” to acknowledge`,
    mismatch: "The name doesn't match this node yet.",
  },

  refreshPrompt: {
    title: "Update the protected-interface list?",
    body:
      "You just moved this node's management address to a new interface. vnprox's safety interlocks track which interface is 'protected' — update that list so it points at the new interface. Declining is fine, but the old entry will show as stale until you do.",
    accept: "Update protected interfaces",
    decline: "Not now",
    updated: "Protected interfaces updated.",
    failed: "Could not update protected interfaces.",
  },

  common: {
    draftNotice:
      "This only drafts the change into your changeset drawer — nothing is applied until you open it, review the exact steps, and apply with the countdown safety net.",
    finishButton: "Add to changeset",
    repeatLabel: "Also do this for other nodes",
    repeatHelp:
      "Stage the same kind of change for other nodes too — each gets its own draft with its own cards picked, so you review and apply them one at a time.",
  },
};
