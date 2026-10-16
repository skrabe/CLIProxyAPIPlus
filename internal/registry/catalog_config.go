package registry

import (
	"fmt"
	"net/url"
	"path/filepath"
)

type CatalogSources struct {
	Catalog string `yaml:"catalog" json:"catalog,omitempty"`
}

func (m CatalogSources) Validate() error {
	if m.Catalog == "" || filepath.IsAbs(m.Catalog) {
		return nil
	}
	parsed, err := url.Parse(m.Catalog)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("models.catalog must be an HTTPS URL or an absolute local path")
	}
	return nil
}
