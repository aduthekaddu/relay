package setup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultReleasesAPI is the GitHub releases endpoint for Relay. The
// RELAY_RELEASES_API environment variable overrides it (forks, mirrors,
// tests).
const DefaultReleasesAPI = "https://api.github.com/repos/aduthekaddu/relay/releases"

// Size caps for downloads.
const (
	maxBinarySize    = 256 << 20
	maxChecksumsSize = 64 << 10
	maxReleaseJSON   = 1 << 20
)

// Release is the part of a GitHub release that updates need.
type Release struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	HTMLURL    string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Asset returns the named asset.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the release file for a platform: relay_<os>_<arch>.
func AssetName(goos, goarch string) string { return "relay_" + goos + "_" + goarch }

// Updater downloads and verifies releases.
type Updater struct {
	HTTP  *http.Client
	API   string // default DefaultReleasesAPI
	Token string // optional GitHub token (rate limits)
}

func (u Updater) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (u Updater) get(ctx context.Context, rawURL string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "relay-update")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if u.Token != "" && isGitHub(rawURL) {
		req.Header.Set("Authorization", "Bearer "+u.Token)
	}
	res, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", redactURL(rawURL), res.Status)
	}
	return res, nil
}

func isGitHub(raw string) bool {
	p, err := url.Parse(raw)
	return err == nil && (p.Host == "api.github.com" || p.Host == "github.com")
}

func redactURL(raw string) string {
	p, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)"
	}
	p.User = nil
	p.RawQuery = ""
	return p.String()
}

// FetchRelease returns the latest release, or the one tagged tag.
func (u Updater) FetchRelease(ctx context.Context, tag string) (*Release, error) {
	api := strings.TrimRight(firstNonEmpty(u.API, DefaultReleasesAPI), "/")
	endpoint := api + "/latest"
	if tag != "" {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		if !validTag(tag) {
			return nil, fmt.Errorf("invalid version %q", tag)
		}
		endpoint = api + "/tags/" + url.PathEscape(tag)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := u.get(ctx, endpoint, "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("look up release: %w", err)
	}
	defer res.Body.Close()
	var rel Release
	if err := json.NewDecoder(io.LimitReader(res.Body, maxReleaseJSON)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("look up release: %w", err)
	}
	if rel.Tag == "" || !validTag(rel.Tag) {
		return nil, fmt.Errorf("look up release: unexpected tag %q", rel.Tag)
	}
	return &rel, nil
}

func validTag(t string) bool {
	if len(t) < 2 || len(t) > 64 || t[0] != 'v' {
		return false
	}
	for _, r := range t[1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-' || r == '+') {
			return false
		}
	}
	return true
}

// ParseChecksums reads a sha256sum-style file ("<hex>  <name>" or
// "<hex> *<name>") into name → lowercase hex.
func ParseChecksums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("checksums: malformed line %q", line)
		}
		sum := strings.ToLower(f[0])
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			return nil, fmt.Errorf("checksums: bad sha256 %q", f[0])
		}
		out[strings.TrimPrefix(f[1], "*")] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("checksums: empty file")
	}
	return out, nil
}

// Download fetches the platform asset of rel into a new file in dir (so
// it can be renamed over the old binary atomically), verifying its
// sha256 against the release's checksums.txt. The caller removes the
// file on failure paths it takes afterwards.
func (u Updater) Download(ctx context.Context, rel *Release, asset, dir string) (string, error) {
	bin, ok := rel.Asset(asset)
	if !ok {
		return "", fmt.Errorf("release %s has no %s asset", rel.Tag, asset)
	}
	sumsAsset, ok := rel.Asset("checksums.txt")
	if !ok {
		return "", fmt.Errorf("release %s has no checksums.txt; refusing to install an unverified binary", rel.Tag)
	}
	res, err := u.get(ctx, sumsAsset.URL, "")
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxChecksumsSize))
	res.Body.Close()
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	sums, err := ParseChecksums(data)
	if err != nil {
		return "", err
	}
	want, ok := sums[asset]
	if !ok {
		return "", fmt.Errorf("checksums.txt has no entry for %s", asset)
	}
	res, err = u.get(ctx, bin.URL, "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	defer res.Body.Close()
	f, err := os.CreateTemp(dir, ".relay-update-*")
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	name := f.Name()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxBinarySize+1))
	cerr := f.Close()
	if err == nil && n > maxBinarySize {
		err = fmt.Errorf("%s is larger than %d MiB", asset, maxBinarySize>>20)
	}
	if err = errors.Join(err, cerr); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(name)
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}
	if err := os.Chmod(name, 0o755); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// VerifyRuns executes `<bin> version` to make sure the new binary starts
// on this machine (right OS/arch, not truncated) and reports tag.
func VerifyRuns(ctx context.Context, bin, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("new binary does not run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if tag != "" && !strings.Contains(string(out), tag) {
		return fmt.Errorf("new binary reports %q, expected %s", strings.TrimSpace(string(out)), tag)
	}
	return nil
}

// Replace atomically renames newBin over target. Running processes keep
// the old inode, so a live `relay serve` is unaffected until restarted.
func Replace(newBin, target string) error {
	if filepath.Dir(newBin) != filepath.Dir(target) {
		return fmt.Errorf("replace: %s and %s must be in the same directory", newBin, target)
	}
	if err := os.Rename(newBin, target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

// CompareVersions compares two versions like v1.2.3 or 1.2.3-rc.1 and
// returns -1, 0 or +1. A release sorts after its pre-releases; "dev" and
// unparsable versions sort before everything.
func CompareVersions(a, b string) int {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa.nums[i] != pb.nums[i] {
			if pa.nums[i] < pb.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	return comparePre(pa.pre, pb.pre)
}

type semver struct {
	nums [3]int
	pre  string
}

func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v semver
	core, pre, _ := strings.Cut(s, "-")
	v.pre = pre
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.nums[i] = n
	}
	return v, true
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		na, ea := strconv.Atoi(as[i])
		nb, eb := strconv.Atoi(bs[i])
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}
