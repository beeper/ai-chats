package openclaw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const openClawGatewayServiceType = "_openclaw-gw._tcp"

type openClawDiscoveredGateway struct {
	StableID                    string
	Source                      string
	Domain                      string
	InstanceName                string
	DisplayName                 string
	GatewayURL                  string
	ServiceHost                 string
	ServicePort                 int
	LanHost                     string
	TailnetDNS                  string
	GatewayTLS                  bool
	GatewayTLSFingerprintSHA256 string
	SSHPort                     int
	CLIPath                     string
}

type openClawDiscoveryOptions struct {
	Timeout         time.Duration
	WideAreaEnabled bool
	WideAreaDomain  string
}

type gatewayBonjourBeacon struct {
	InstanceName                string
	Domain                      string
	DisplayName                 string
	Host                        string
	Port                        int
	LanHost                     string
	TailnetDNS                  string
	GatewayPort                 int
	SSHPort                     int
	GatewayTLS                  bool
	GatewayTLSFingerprintSHA256 string
	CLIPath                     string
}

type discoveryCommandRunner func(ctx context.Context, name string, args ...string) (stdout string, stderr string, err error)

func defaultDiscoveryCommandRunner(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func normalizeDiscoveryTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 2 * time.Second
	}
	return timeout
}

func normalizeServiceDomain(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" || trimmed == "local" || trimmed == "local." {
		return "local."
	}
	if strings.HasSuffix(trimmed, ".") {
		return trimmed
	}
	return trimmed + "."
}

func discoveryDomains(opts openClawDiscoveryOptions) []string {
	domains := []string{"local."}
	if opts.WideAreaEnabled {
		if wide := normalizeServiceDomain(opts.WideAreaDomain); wide != "local." {
			domains = append(domains, wide)
		}
	}
	return domains
}

func discoverOpenClawGateways(ctx context.Context, opts openClawDiscoveryOptions) ([]openClawDiscoveredGateway, error) {
	return discoverOpenClawGatewaysWithRunner(ctx, opts, defaultDiscoveryCommandRunner)
}

func discoverOpenClawGatewaysWithRunner(ctx context.Context, opts openClawDiscoveryOptions, run discoveryCommandRunner) ([]openClawDiscoveredGateway, error) {
	timeout := normalizeDiscoveryTimeout(opts.Timeout)
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		beacons  []gatewayBonjourBeacon
		firstErr error
	)
	for _, domain := range discoveryDomains(opts) {
		discoverCtx, cancel := context.WithTimeout(ctx, timeout)
		var domainBeacons []gatewayBonjourBeacon
		var err error
		switch runtime.GOOS {
		case "darwin":
			domainBeacons, err = discoverViaDNSSD(discoverCtx, domain, run)
		case "linux":
			domainBeacons, err = discoverViaAvahi(discoverCtx, domain, run)
		default:
			cancel()
			return nil, nil
		}
		cancel()
		if err != nil && firstErr == nil {
			firstErr = err
		}
		beacons = append(beacons, domainBeacons...)
	}
	results := dedupeDiscoveredGateways(mapDiscoveredGateways(beacons))
	if len(results) == 0 {
		return nil, firstErr
	}
	return results, nil
}

func mapDiscoveredGateways(beacons []gatewayBonjourBeacon) []openClawDiscoveredGateway {
	out := make([]openClawDiscoveredGateway, 0, len(beacons))
	for _, beacon := range beacons {
		host := strings.TrimSpace(beacon.Host)
		if host == "" {
			host = strings.TrimSpace(beacon.TailnetDNS)
		}
		if host == "" {
			host = strings.TrimSpace(beacon.LanHost)
		}
		port := beacon.Port
		if port <= 0 {
			port = beacon.GatewayPort
		}
		if host == "" || port <= 0 {
			continue
		}
		scheme := "ws"
		if beacon.GatewayTLS {
			scheme = "wss"
		}
		domain := normalizeServiceDomain(beacon.Domain)
		source := "mdns"
		if domain != "local." {
			source = "wide_area"
		}
		displayName := strings.TrimSpace(beacon.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(beacon.InstanceName)
		}
		stableID := fmt.Sprintf("%s|%s|%s|%s|%d", source, domain, strings.TrimSpace(beacon.InstanceName), host, port)
		out = append(out, openClawDiscoveredGateway{
			StableID:                    stableID,
			Source:                      source,
			Domain:                      domain,
			InstanceName:                strings.TrimSpace(beacon.InstanceName),
			DisplayName:                 displayName,
			GatewayURL:                  fmt.Sprintf("%s://%s:%d", scheme, host, port),
			ServiceHost:                 strings.TrimSpace(beacon.Host),
			ServicePort:                 beacon.Port,
			LanHost:                     strings.TrimSpace(beacon.LanHost),
			TailnetDNS:                  strings.TrimSpace(beacon.TailnetDNS),
			GatewayTLS:                  beacon.GatewayTLS,
			GatewayTLSFingerprintSHA256: strings.TrimSpace(beacon.GatewayTLSFingerprintSHA256),
			SSHPort:                     beacon.SSHPort,
			CLIPath:                     strings.TrimSpace(beacon.CLIPath),
		})
	}
	return out
}

func dedupeDiscoveredGateways(gateways []openClawDiscoveredGateway) []openClawDiscoveredGateway {
	if len(gateways) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(gateways))
	out := make([]openClawDiscoveredGateway, 0, len(gateways))
	for _, gateway := range gateways {
		if gateway.StableID == "" {
			continue
		}
		if _, ok := seen[gateway.StableID]; ok {
			continue
		}
		seen[gateway.StableID] = struct{}{}
		out = append(out, gateway)
	}
	slices.SortFunc(out, func(a, b openClawDiscoveredGateway) int {
		if cmp := strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.GatewayURL, b.GatewayURL)
	})
	return out
}

func discoverViaDNSSD(ctx context.Context, domain string, run discoveryCommandRunner) ([]gatewayBonjourBeacon, error) {
	if _, err := exec.LookPath("dns-sd"); err != nil {
		return nil, nil
	}
	stdout, _, browseErr := run(ctx, "dns-sd", "-B", openClawGatewayServiceType, domain)
	instances := parseDnsSdBrowse(stdout)
	if len(instances) == 0 {
		return nil, browseErr
	}
	results := make([]gatewayBonjourBeacon, 0, len(instances))
	for _, instance := range instances {
		resolveCtx, cancel := context.WithTimeout(ctx, time.Second)
		resolveStdout, _, err := run(resolveCtx, "dns-sd", "-L", instance, openClawGatewayServiceType, domain)
		cancel()
		if err != nil && strings.TrimSpace(resolveStdout) == "" {
			continue
		}
		beacon, ok := parseDnsSdResolve(resolveStdout, instance, domain)
		if ok {
			results = append(results, beacon)
		}
	}
	if len(results) == 0 {
		return nil, browseErr
	}
	return results, nil
}

func discoverViaAvahi(ctx context.Context, domain string, run discoveryCommandRunner) ([]gatewayBonjourBeacon, error) {
	if _, err := exec.LookPath("avahi-browse"); err != nil {
		return nil, nil
	}
	args := []string{"-rt", openClawGatewayServiceType}
	if domain != "" && domain != "local." {
		args = append(args, "-d", strings.TrimSuffix(domain, "."))
	}
	stdout, _, err := run(ctx, "avahi-browse", args...)
	results := parseAvahiBrowse(stdout, domain)
	if len(results) == 0 {
		return nil, err
	}
	return results, nil
}

func decodeDnsSdEscapes(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+3 < len(value) {
			escaped := value[i+1 : i+4]
			if escaped[0] >= '0' && escaped[0] <= '9' && escaped[1] >= '0' && escaped[1] <= '9' && escaped[2] >= '0' && escaped[2] <= '9' {
				if b, err := strconv.Atoi(escaped); err == nil && b >= 0 && b <= 255 {
					out.WriteByte(byte(b))
					i += 3
					continue
				}
			}
		}
		out.WriteByte(value[i])
	}
	return out.String()
}

func parseTxtTokens(tokens []string) map[string]string {
	txt := make(map[string]string, len(tokens))
	for _, token := range tokens {
		idx := strings.Index(token, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(token[:idx])
		value := decodeDnsSdEscapes(strings.TrimSpace(token[idx+1:]))
		if key == "" {
			continue
		}
		txt[key] = value
	}
	return txt
}

func parseDnsSdBrowse(stdout string) []string {
	instances := make([]string, 0, 4)
	seen := make(map[string]struct{})
	re := regexp.MustCompile(`_openclaw-gw\._tcp\.?\s+(.+)$`)
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, openClawGatewayServiceType) || !strings.Contains(line, "Add") {
			continue
		}
		match := re.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		instance := decodeDnsSdEscapes(strings.TrimSpace(match[1]))
		if instance == "" {
			continue
		}
		if _, ok := seen[instance]; ok {
			continue
		}
		seen[instance] = struct{}{}
		instances = append(instances, instance)
	}
	return instances
}

func parseDnsSdResolve(stdout, instanceName, domain string) (gatewayBonjourBeacon, bool) {
	beacon := gatewayBonjourBeacon{
		InstanceName: decodeDnsSdEscapes(strings.TrimSpace(instanceName)),
		Domain:       domain,
	}
	var txt map[string]string
	reachability := regexp.MustCompile(`can be reached at\s+([^\s:]+):(\d+)`)
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if match := reachability.FindStringSubmatch(line); len(match) == 3 {
			beacon.Host = strings.TrimSuffix(strings.TrimSpace(match[1]), ".")
			beacon.Port, _ = strconv.Atoi(match[2])
			continue
		}
		if strings.HasPrefix(line, "txt") || strings.Contains(line, "txtvers=") {
			txt = parseTxtTokens(strings.Fields(line))
		}
	}
	applyTxtToBeacon(&beacon, txt)
	if beacon.DisplayName == "" {
		beacon.DisplayName = beacon.InstanceName
	}
	return beacon, beacon.DisplayName != "" || beacon.Host != ""
}

func parseAvahiBrowse(stdout, domain string) []gatewayBonjourBeacon {
	results := make([]gatewayBonjourBeacon, 0, 4)
	var current *gatewayBonjourBeacon
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "=") && strings.Contains(line, openClawGatewayServiceType) {
			if current != nil {
				results = append(results, *current)
			}
			idx := strings.Index(line, " "+openClawGatewayServiceType)
			left := strings.TrimSpace(line)
			if idx >= 0 {
				left = strings.TrimSpace(line[:idx])
			}
			parts := strings.Fields(left)
			instanceName := left
			if len(parts) > 3 {
				instanceName = strings.Join(parts[3:], " ")
			}
			current = &gatewayBonjourBeacon{
				InstanceName: strings.TrimSpace(instanceName),
				DisplayName:  strings.TrimSpace(instanceName),
				Domain:       domain,
			}
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "hostname ="):
			if match := regexp.MustCompile(`hostname\s*=\s*\[([^\]]+)\]`).FindStringSubmatch(trimmed); len(match) == 2 {
				current.Host = strings.TrimSpace(match[1])
			}
		case strings.HasPrefix(trimmed, "port ="):
			if match := regexp.MustCompile(`port\s*=\s*\[(\d+)\]`).FindStringSubmatch(trimmed); len(match) == 2 {
				current.Port, _ = strconv.Atoi(match[1])
			}
		case strings.HasPrefix(trimmed, "txt ="):
			matches := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(trimmed, -1)
			tokens := make([]string, 0, len(matches))
			for _, match := range matches {
				if len(match) == 2 {
					tokens = append(tokens, match[1])
				}
			}
			applyTxtToBeacon(current, parseTxtTokens(tokens))
		}
	}
	if current != nil {
		results = append(results, *current)
	}
	return results
}

func applyTxtToBeacon(beacon *gatewayBonjourBeacon, txt map[string]string) {
	if beacon == nil || len(txt) == 0 {
		return
	}
	if value := strings.TrimSpace(txt["displayName"]); value != "" {
		beacon.DisplayName = value
	}
	beacon.LanHost = strings.TrimSpace(txt["lanHost"])
	beacon.TailnetDNS = strings.TrimSpace(txt["tailnetDns"])
	beacon.CLIPath = strings.TrimSpace(txt["cliPath"])
	beacon.GatewayPort, _ = strconv.Atoi(strings.TrimSpace(txt["gatewayPort"]))
	beacon.SSHPort, _ = strconv.Atoi(strings.TrimSpace(txt["sshPort"]))
	if raw := strings.ToLower(strings.TrimSpace(txt["gatewayTls"])); raw == "1" || raw == "true" || raw == "yes" {
		beacon.GatewayTLS = true
	}
	beacon.GatewayTLSFingerprintSHA256 = strings.TrimSpace(txt["gatewayTlsSha256"])
}

var errWideAreaDomainRequired = errors.New("wide-area discovery requested but no wide-area domain is configured")
