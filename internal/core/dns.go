package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ServeDNS answers DNS questions for names that end in one of the TLDs:
// A gives 127.0.0.1 and AAAA gives ::1. macOS sends only those names here,
// because of the files in /etc/resolver. It blocks until an error.
func ServeDNS(port int, tlds []string) error {
	conn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer conn.Close()
	buf := make([]byte, 512)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		if resp := dnsAnswer(buf[:n], tlds); resp != nil {
			conn.WriteTo(resp, addr)
		}
	}
}

// dnsAnswer builds the reply to one query, or nil for a packet that is not
// a query.
func dnsAnswer(q []byte, tlds []string) []byte {
	if len(q) < 12 || q[2]&0x80 != 0 || binary.BigEndian.Uint16(q[4:6]) != 1 {
		return nil
	}
	// Read the question name.
	var labels []string
	i := 12
	for {
		if i >= len(q) {
			return nil
		}
		l := int(q[i])
		if l == 0 {
			i++
			break
		}
		if l > 63 || i+1+l > len(q) {
			return nil
		}
		labels = append(labels, strings.ToLower(string(q[i+1:i+1+l])))
		i += 1 + l
	}
	if i+4 > len(q) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(q[i : i+2])
	question := q[12 : i+4]
	name := strings.Join(labels, ".")

	match := false
	for _, t := range tlds {
		if name == t || strings.HasSuffix(name, "."+t) {
			match = true
		}
	}
	resp := make([]byte, 12, 12+len(question)+28)
	copy(resp, q[:2])                        // ID
	resp[2] = 0x84 | (q[2] & 0x01)           // response, authoritative, copy "recursion desired"
	resp[3] = 0x80                           // recursion available, no error
	binary.BigEndian.PutUint16(resp[4:6], 1) // one question
	if !match {
		resp[3] |= 3 // NXDOMAIN
		return append(resp, question...)
	}
	resp = append(resp, question...)
	var rdata []byte
	switch qtype {
	case 1: // A
		rdata = net.IPv4(127, 0, 0, 1).To4()
	case 28: // AAAA
		rdata = net.IPv6loopback
	}
	if rdata == nil {
		return resp // the name exists, but has no record of this type
	}
	binary.BigEndian.PutUint16(resp[6:8], 1) // one answer
	rr := []byte{0xc0, 0x0c}                 // the name in the question
	rr = binary.BigEndian.AppendUint16(rr, qtype)
	rr = binary.BigEndian.AppendUint16(rr, 1)  // class IN
	rr = binary.BigEndian.AppendUint32(rr, 60) // TTL
	rr = binary.BigEndian.AppendUint16(rr, uint16(len(rdata)))
	return append(append(resp, rr...), rdata...)
}

// dnsQuery asks the server on a port for the A record of a name.
func dnsQuery(port int, name string) (net.IP, error) {
	conn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	q := []byte{0x12, 0x34, 0x01, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range strings.Split(name, ".") {
		q = append(append(q, byte(len(l))), l...)
	}
	q = append(q, 0, 0, 1, 0, 1)
	conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < len(q)+16 || binary.BigEndian.Uint16(buf[6:8]) == 0 {
		return nil, errors.New("no answer")
	}
	return net.IP(buf[n-4 : n]), nil
}

// dnsService runs "benchtop dns-serve" as a child process.
type dnsService struct{}

func (s *dnsService) ID() string       { return "dns" }
func (s *dnsService) Name() string     { return "Wildcard domains" }
func (s *dnsService) Kind() string     { return "dns" }
func (s *dnsService) Formula() string  { return "" }
func (s *dnsService) Installed() bool  { return true }
func (s *dnsService) Version() string  { return "" }
func (s *dnsService) DefaultPort() int { return 53535 }

// DNSTLDs are the domain endings that the DNS server answers. The default is "test".
func (m *Manager) DNSTLDs() []string {
	if d := m.serviceSettings("dns").Domains; len(d) > 0 {
		return d
	}
	return []string{"test"}
}

func resolverFile(tld string) string { return filepath.Join("/etc/resolver", tld) }

func resolverText(port int) string {
	return fmt.Sprintf("# Created by Benchtop for wildcard domains.\nnameserver 127.0.0.1\nport %d\n", port)
}

// resolverReady tells if macOS sends names under the TLD to Benchtop.
func (m *Manager) resolverReady(tld string) bool {
	data, err := os.ReadFile(resolverFile(tld))
	return err == nil && string(data) == resolverText(m.servicePortOrDefault(&dnsService{}))
}

func (s *dnsService) prepare(m *Manager) error { return nil }

func (s *dnsService) command(m *Manager) (string, []string) {
	exe, _ := executable()
	return exe, []string{"dns-serve", "--port", fmt.Sprint(m.servicePortOrDefault(s)), "--tld", strings.Join(m.DNSTLDs(), ",")}
}

func (s *dnsService) ready(m *Manager) bool {
	ip, err := dnsQuery(m.servicePortOrDefault(s), "benchtop-check."+m.DNSTLDs()[0])
	return err == nil && ip.Equal(net.IPv4(127, 0, 0, 1))
}

// afterStart writes the /etc/resolver files, once, with the password dialog.
func (s *dnsService) afterStart(m *Manager) error {
	var cmds []string
	tmp := m.path("tmp", "resolver")
	for _, tld := range m.DNSTLDs() {
		if m.resolverReady(tld) {
			continue
		}
		if err := os.WriteFile(tmp, []byte(resolverText(m.servicePortOrDefault(s))), 0o644); err != nil {
			return err
		}
		cmds = append(cmds, "/bin/mkdir -p /etc/resolver", fmt.Sprintf("/bin/cp %s %s", shQuote(tmp), shQuote(resolverFile(tld))))
	}
	if len(cmds) == 0 {
		return nil
	}
	cmds = append(cmds, "/usr/bin/dscacheutil -flushcache", "(/usr/bin/killall -HUP mDNSResponder || true)")
	return runAdmin("Benchtop needs your password to send ."+strings.Join(m.DNSTLDs(), ", .")+" domains to its DNS server. This is a one-time step.", cmds...)
}

// afterStop removes the resolver files when the user turns the feature off.
func (s *dnsService) afterStop(m *Manager, remember bool) error {
	if !remember {
		return nil
	}
	var cmds []string
	for _, tld := range m.DNSTLDs() {
		if m.resolverReady(tld) {
			cmds = append(cmds, "/bin/rm -f "+shQuote(resolverFile(tld)))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return runAdmin("Benchtop needs your password to remove its /etc/resolver files.", cmds...)
}

func (s *dnsService) detail(m *Manager) string {
	var parts []string
	for _, t := range m.DNSTLDs() {
		parts = append(parts, "*."+t)
	}
	return strings.Join(parts, ", ") + " → this Mac"
}

func (s *dnsService) files(m *Manager) []ConfigFile { return nil }
func (s *dnsService) links(m *Manager) []Link       { return nil }
func (s *dnsService) env(m *Manager) string         { return "" }

// wildcardCovers tells if the wildcard DNS answers for a domain, so the
// domain needs no /etc/hosts entry.
func (m *Manager) wildcardCovers(domain string) bool {
	if _, running := m.extraRunning("dns"); !running {
		return false
	}
	for _, t := range m.DNSTLDs() {
		if strings.HasSuffix(domain, "."+t) && m.resolverReady(t) {
			return true
		}
	}
	return false
}
