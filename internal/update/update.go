// Package update checks public release metadata, never sending WeRead credentials.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const InstallCommand = "go install github.com/songquanpeng/weread-cli/cmd/weread-cli@latest"
const LatestURL = "https://api.github.com/repos/songquanpeng/weread-cli/releases/latest"

type Status struct {
	Current         string `json:"current_version"`
	Latest          string `json:"latest_version,omitempty"`
	State           string `json:"state"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateCommand   string `json:"update_command"`
	Error           string `json:"error,omitempty"`
}

type Checker struct {
	URL  string
	HTTP *http.Client
}

func (c Checker) Check(ctx context.Context, current string, offline bool) Status {
	v := Status{Current: current, State: "offline", UpdateCommand: InstallCommand}
	if offline {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoint := c.URL
	if endpoint == "" {
		endpoint = LatestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		v.State = "unknown"
		v.Error = "invalid release endpoint"
		return v
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "weread-cli")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		v.State = "unknown"
		v.Error = "could not check GitHub releases; retry weread-cli status"
		return v
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		v.State = "unknown"
		v.Error = fmt.Sprintf("release check HTTP %d; retry weread-cli status", resp.StatusCode)
		return v
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release) != nil || release.Tag == "" {
		v.State = "unknown"
		v.Error = "invalid GitHub release metadata"
		return v
	}
	if _, ok := parseVersion(release.Tag); !ok {
		v.State = "unknown"
		v.Error = "unrecognized release version"
		return v
	}
	v.Latest = release.Tag
	if older, ok := Older(current, release.Tag); ok {
		v.UpdateAvailable = older
		v.State = "current"
		if older {
			v.State = "outdated"
		}
	} else {
		v.State = "development"
	}
	return v
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(s, "v")
	if strings.ContainsAny(s, "-+") {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func Older(current, latest string) (bool, bool) {
	a, ok := parseVersion(current)
	if !ok {
		return false, false
	}
	b, ok := parseVersion(latest)
	if !ok {
		return false, false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i], true
		}
	}
	return false, true
}
