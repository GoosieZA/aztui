package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const latestReleaseURL = "https://api.github.com/repos/GoosieZA/aztui/releases/latest"

// LatestRelease asks GitHub for the newest published tag (e.g. "v0.5.0").
// One unauthenticated request — well inside GitHub's rate limits for a
// once-per-launch check.
func LatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aztui/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github responded %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// IsReleaseBuild reports whether this binary came from a tagged release —
// dev and dirty builds skip the update check to avoid noise.
func IsReleaseBuild() bool {
	return !strings.Contains(Version, "dev") && !strings.Contains(Version, "dirty")
}

// IsNewer reports whether latest is a strictly newer semver than current.
// Unparseable versions never count as newer.
func IsNewer(latest, current string) bool {
	lp, lok := parseSemver(latest)
	cp, cok := parseSemver(current)
	if !lok || !cok {
		return false
	}
	for i := 0; i < 3; i++ {
		if lp[i] != cp[i] {
			return lp[i] > cp[i]
		}
	}
	return false
}

func parseSemver(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
