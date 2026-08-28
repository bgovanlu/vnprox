// SPDX-License-Identifier: Apache-2.0

package fw

import "sort"

// MacroPort is one proto/port pair a macro expands to.
type MacroPort struct {
	Proto string // "tcp" | "udp" | "icmp" | ""
	Dport string // e.g. "80", "465,587", "1024:65535" — empty for protocols with no ports (icmp)
}

// Macro is a named, built-in pve-firewall service macro (docs/features/
// firewall.md §2's "macro picker (HTTP, SMTP, ...) with expansion
// preview") and the proto/port pairs it expands to.
type Macro struct {
	Name    string
	Comment string
	Ports   []MacroPort
}

// macroCatalog is a practical, representative subset of pve-firewall's
// built-in macros (real pve-firewall ships several dozen, defined in its
// own Macros.pm; this is not an exhaustive port of that list — it covers
// the common services this task's fixtures and the acceptance criterion
// 4 ("macro definitions render expansion previews") need to demonstrate).
// Expansions match pve-firewall's own documented definitions for these
// specific macros.
var macroCatalog = map[string]Macro{
	"HTTP":       {Name: "HTTP", Comment: "Web traffic (HTTP)", Ports: []MacroPort{{Proto: "tcp", Dport: "80"}}},
	"HTTPS":      {Name: "HTTPS", Comment: "Web traffic (HTTPS)", Ports: []MacroPort{{Proto: "tcp", Dport: "443"}}},
	"SSH":        {Name: "SSH", Comment: "Secure shell", Ports: []MacroPort{{Proto: "tcp", Dport: "22"}}},
	"DNS":        {Name: "DNS", Comment: "Domain Name System", Ports: []MacroPort{{Proto: "udp", Dport: "53"}, {Proto: "tcp", Dport: "53"}}},
	"SMTP":       {Name: "SMTP", Comment: "Mail transport", Ports: []MacroPort{{Proto: "tcp", Dport: "25"}}},
	"SMTPS":      {Name: "SMTPS", Comment: "Mail transport over TLS", Ports: []MacroPort{{Proto: "tcp", Dport: "465"}}},
	"IMAP":       {Name: "IMAP", Comment: "Mail retrieval", Ports: []MacroPort{{Proto: "tcp", Dport: "143"}}},
	"IMAPS":      {Name: "IMAPS", Comment: "Mail retrieval over TLS", Ports: []MacroPort{{Proto: "tcp", Dport: "993"}}},
	"POP3":       {Name: "POP3", Comment: "Mail retrieval", Ports: []MacroPort{{Proto: "tcp", Dport: "110"}}},
	"POP3S":      {Name: "POP3S", Comment: "Mail retrieval over TLS", Ports: []MacroPort{{Proto: "tcp", Dport: "995"}}},
	"FTP":        {Name: "FTP", Comment: "File transfer (control)", Ports: []MacroPort{{Proto: "tcp", Dport: "21"}}},
	"NTP":        {Name: "NTP", Comment: "Network time", Ports: []MacroPort{{Proto: "udp", Dport: "123"}}},
	"Ping":       {Name: "Ping", Comment: "ICMP echo", Ports: []MacroPort{{Proto: "icmp"}}},
	"Telnet":     {Name: "Telnet", Comment: "Unencrypted remote shell", Ports: []MacroPort{{Proto: "tcp", Dport: "23"}}},
	"Rsync":      {Name: "Rsync", Comment: "File sync", Ports: []MacroPort{{Proto: "tcp", Dport: "873"}}},
	"VNC":        {Name: "VNC", Comment: "Remote console (single display range)", Ports: []MacroPort{{Proto: "tcp", Dport: "5900:5999"}}},
	"Syslog":     {Name: "Syslog", Comment: "Log forwarding", Ports: []MacroPort{{Proto: "udp", Dport: "514"}, {Proto: "tcp", Dport: "514"}}},
	"SNMP":       {Name: "SNMP", Comment: "Network monitoring", Ports: []MacroPort{{Proto: "udp", Dport: "161"}}},
	"LDAP":       {Name: "LDAP", Comment: "Directory service", Ports: []MacroPort{{Proto: "tcp", Dport: "389"}}},
	"LDAPS":      {Name: "LDAPS", Comment: "Directory service over TLS", Ports: []MacroPort{{Proto: "tcp", Dport: "636"}}},
	"HKP":        {Name: "HKP", Comment: "OpenPGP key server", Ports: []MacroPort{{Proto: "tcp", Dport: "11371"}}},
	"MySQL":      {Name: "MySQL", Comment: "MySQL/MariaDB", Ports: []MacroPort{{Proto: "tcp", Dport: "3306"}}},
	"PostgreSQL": {Name: "PostgreSQL", Comment: "PostgreSQL", Ports: []MacroPort{{Proto: "tcp", Dport: "5432"}}},
}

// MacroExpansion returns the named macro's proto/port expansion preview
// (docs/features/firewall.md §2), and whether name is a known macro.
// Lookup is case-sensitive, matching pve-firewall's own macro names
// exactly as they appear in a rule's "macro" field.
func MacroExpansion(name string) (Macro, bool) {
	m, ok := macroCatalog[name]
	return m, ok
}

// KnownMacros returns every built-in macro this package knows, sorted by
// name, for the macro picker's listing.
func KnownMacros() []Macro {
	names := make([]string, 0, len(macroCatalog))
	for n := range macroCatalog {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Macro, len(names))
	for i, n := range names {
		out[i] = macroCatalog[n]
	}
	return out
}
