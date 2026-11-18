//go:build darwin

package dns

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	tailscaledns "tailscale.com/net/dns"
	"tailscale.com/util/dnsname"
)

const platformGOOS = "darwin"

const (
	defaultResolverDir        = "/etc/resolver"
	anyscaleSearchResolverFile = "search.anyscale"
	anyscaleResolverFileHeader = "# This file is managed by anyscale.\n"
)

// splitDNSConfigurator makes Manager compile MagicDNS as native split DNS.
// The stock Darwin Manager blends the machine's base resolvers into quad-100
// because some Apple DNS APIs cannot express selective local records. anyscale only
// serves complete tailnet suffixes, so /etc/resolver is the better fit here.
type splitDNSConfigurator struct {
	tailscaledns.OSConfigurator
	resolverDir string
}

func (c *splitDNSConfigurator) SetDNS(cfg tailscaledns.OSConfig) error {
	global := len(cfg.Nameservers) > 0 && len(cfg.MatchDomains) == 0
	osCfg := cfg
	if global {
		// The synthetic global resolver's SearchDomains do not reliably
		// expand single-label names. Keep using the /etc/resolver search
		// mechanism that macOS uses for anyscale's split-DNS configuration.
		osCfg.SearchDomains = nil
	}
	if err := c.OSConfigurator.SetDNS(osCfg); err != nil {
		return err
	}
	if global && len(cfg.SearchDomains) > 0 {
		return c.writeSearchDomains(cfg.SearchDomains)
	}
	return c.removeSearchDomains()
}

func (*splitDNSConfigurator) GetBaseConfig() (tailscaledns.OSConfig, error) {
	return tailscaledns.OSConfig{}, tailscaledns.ErrGetBaseConfigNotSupported
}

func (c *splitDNSConfigurator) Close() error {
	return errors.Join(c.OSConfigurator.Close(), c.removeSearchDomains())
}

func (c *splitDNSConfigurator) writeSearchDomains(domains []dnsname.FQDN) error {
	dir := c.resolverDirectory()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	var content strings.Builder
	content.WriteString(anyscaleResolverFileHeader)
	content.WriteString("search")
	for _, domain := range domains {
		content.WriteByte(' ')
		content.WriteString(string(domain.WithoutTrailingDot()))
	}
	content.WriteByte('\n')
	return root.WriteFile(anyscaleSearchResolverFile, []byte(content.String()), 0644)
}

func (c *splitDNSConfigurator) removeSearchDomains() error {
	root, err := os.OpenRoot(c.resolverDirectory())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(anyscaleSearchResolverFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (c *splitDNSConfigurator) resolverDirectory() string {
	if c.resolverDir != "" {
		return c.resolverDir
	}
	return defaultResolverDir
}

func platformOSConfigurator(configurator tailscaledns.OSConfigurator) tailscaledns.OSConfigurator {
	return &splitDNSConfigurator{
		OSConfigurator: configurator,
		resolverDir:    defaultResolverDir,
	}
}
