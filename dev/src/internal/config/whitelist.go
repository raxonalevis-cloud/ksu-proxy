package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

type WhitelistFile struct {
	Packages []PackageRule `json:"packages"`
}

type PackageRule struct {
	PackageName    string `json:"package_name"`
	Scope          string `json:"scope"`
	IncludeUserIDs []int  `json:"include_user_ids,omitempty"`
	ExcludeUserIDs []int  `json:"exclude_user_ids,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
}

func LoadWhitelist(path string) (WhitelistFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WhitelistFile{}, nil
		}
		return WhitelistFile{}, err
	}
	var wl WhitelistFile
	if err := json.Unmarshal(raw, &wl); err != nil {
		return WhitelistFile{}, err
	}
	return wl, nil
}

func SaveWhitelist(path string, wl WhitelistFile) error {
	normalized := normalizeWhitelist(wl)
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".packages-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0640); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	marker := filepath.Join(filepath.Dir(path), ".user_modified")
	return os.WriteFile(marker, []byte("1\n"), 0640)
}

func (w WhitelistFile) EnabledPackages() []PackageRule {
	out := make([]PackageRule, 0, len(w.Packages))
	for _, item := range w.Packages {
		if item.PackageName == "" {
			continue
		}
		if item.Enabled != nil && !*item.Enabled {
			continue
		}
		if item.Scope == "" {
			item.Scope = "all_instances"
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PackageName < out[j].PackageName
	})
	return out
}

func normalizeWhitelist(wl WhitelistFile) WhitelistFile {
	seen := make(map[string]bool)
	out := make([]PackageRule, 0, len(wl.Packages))
	for _, item := range wl.Packages {
		if item.PackageName == "" || seen[item.PackageName] {
			continue
		}
		if item.Scope == "" {
			item.Scope = "all_instances"
		}
		seen[item.PackageName] = true
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PackageName < out[j].PackageName
	})
	return WhitelistFile{Packages: out}
}
