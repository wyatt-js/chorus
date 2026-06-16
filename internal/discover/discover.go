// Package discover finds AirPlay (RAOP) speakers on the local network via mDNS.
package discover

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// DNS-SD service types we browse.
const (
	raopService = "_raop._tcp"       // classic AirPlay audio receivers
	castService = "_googlecast._tcp" // Google Cast devices
)

// CastDevice is a discovered Google Cast device.
type CastDevice struct {
	Name string            // friendly name (TXT "fn"), falling back to the instance
	Host net.IP            // resolved IPv4 address
	Port int               // Cast port (usually 8009)
	TXT  map[string]string // parsed TXT record (fn, md, id, ...)
}

// BrowseCast listens for Google Cast devices for the given duration and returns
// the unique set found, sorted by name.
func BrowseCast(ctx context.Context, wait time.Duration) ([]CastDevice, error) {
	entries, err := browse(ctx, castService, wait)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]CastDevice)
	for entry := range entries {
		ip := firstIPv4(entry.AddrIPv4)
		if ip == nil {
			continue
		}
		txt := parseTXT(entry.Text)
		name := txt["fn"]
		if name == "" {
			name = friendlyName(entry.Instance)
		}
		byName[name] = CastDevice{Name: name, Host: ip, Port: entry.Port, TXT: txt}
	}

	devices := make([]CastDevice, 0, len(byName))
	for _, d := range byName {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, nil
}

// Speaker is a discovered AirPlay audio receiver.
type Speaker struct {
	Name string            // friendly name (TXT/instance, "@" prefix stripped)
	Host net.IP            // resolved IPv4 address
	Port int               // RAOP control port (from the SRV record)
	TXT  map[string]string // parsed TXT record (et, md, cn, ch, sr, ss, am, ...)
}

// browse runs an mDNS browse for service over the given window and returns a
// channel that closes when the window elapses.
func browse(ctx context.Context, service string, wait time.Duration) (<-chan *zeroconf.ServiceEntry, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry, 16)
	browseCtx, cancel := context.WithTimeout(ctx, wait)
	if err := resolver.Browse(browseCtx, service, "local.", entries); err != nil {
		cancel()
		return nil, err
	}
	// cancel fires on timeout; zeroconf closes entries when browseCtx is done.
	go func() { <-browseCtx.Done(); cancel() }()
	return entries, nil
}

// Browse listens for RAOP speakers for the given duration and returns the unique
// set found, sorted by name.
func Browse(ctx context.Context, wait time.Duration) ([]Speaker, error) {
	entries, err := browse(ctx, raopService, wait)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]Speaker)
	for entry := range entries {
		ip := firstIPv4(entry.AddrIPv4)
		if ip == nil {
			continue // skip IPv6-only / unresolved entries for Phase 0
		}
		spk := Speaker{
			Name: friendlyName(entry.Instance),
			Host: ip,
			Port: entry.Port,
			TXT:  parseTXT(entry.Text),
		}
		byName[spk.Name] = spk
	}

	speakers := make([]Speaker, 0, len(byName))
	for _, s := range byName {
		speakers = append(speakers, s)
	}
	sort.Slice(speakers, func(i, j int) bool { return speakers[i].Name < speakers[j].Name })
	return speakers, nil
}

// firstIPv4 returns the first usable IPv4 address from the list.
func firstIPv4(addrs []net.IP) net.IP {
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4
		}
	}
	return nil
}

// friendlyName strips the "DEADBEEFCAFE@" hardware-id prefix RAOP instances use
// and decodes DNS presentation-format escaping (e.g. "\ " -> space,
// "\226\128\153" -> the UTF-8 apostrophe).
func friendlyName(instance string) string {
	name := unescapeDNS(instance)
	if i := strings.Index(name, "@"); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

// unescapeDNS decodes DNS presentation-format escapes: "\DDD" is a decimal byte
// value, "\x" is a literal x.
func unescapeDNS(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			continue
		}
		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			out = append(out, (s[i+1]-'0')*100+(s[i+2]-'0')*10+(s[i+3]-'0'))
			i += 3
		} else {
			out = append(out, s[i+1])
			i++
		}
	}
	return string(out)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// parseTXT turns "key=value" TXT entries into a map.
func parseTXT(txt []string) map[string]string {
	m := make(map[string]string, len(txt))
	for _, kv := range txt {
		if i := strings.Index(kv, "="); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		} else {
			m[kv] = ""
		}
	}
	return m
}
