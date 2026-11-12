package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
)

// ErrNoBundle means the channel has no bundle for this platform.
var ErrNoBundle = errors.New("update: no bundle for this platform")

// Source is one channel of signed bundles (ADR-0018 §2): TUF metadata at
// MetadataURL, trusted from Root on; bundles wherever their signed target
// says. Both hosts are untrusted transports.
type Source struct {
	MetadataURL string // <update_url>/<channel>
	Root        []byte // the root.json the Hive shipped with
	CacheDir    string // trusted metadata kept between checks
	HTTP        *http.Client
}

// Offer is the signed bundle a channel has for a platform.
type Offer struct {
	Target Target
	URL    string
	file   *metadata.TargetFiles
}

// bundleCustom are the custom fields of a bundle target.
type bundleCustom struct {
	Version    string `json:"version"`
	URL        string `json:"url"`
	Security   bool   `json:"security"`
	Rollout    int    `json:"rollout"`
	MinUpdater string `json:"min_updater"`
}

// BundleTarget is the target name of a platform's bundle, e.g. bundles/linux_arm64.
func BundleTarget(platform string) string { return "bundles/" + platform }

// Offer refreshes the channel's metadata (root rotation, expiry, rollback
// and freeze protection by TUF) and returns the platform's bundle.
func (s Source) Offer(_ context.Context, platform string) (Offer, error) {
	cfg, err := config.New(s.MetadataURL, trustedRoot(s.Root, s.CacheDir))
	if err != nil {
		return Offer{}, err
	}
	cfg.LocalMetadataDir = s.CacheDir
	cfg.LocalTargetsDir = filepath.Join(s.CacheDir, "targets") // unused: bundles are fetched by Download
	if s.HTTP != nil {
		if err := cfg.SetDefaultFetcherHTTPClient(s.HTTP); err != nil {
			return Offer{}, err
		}
	}
	if err := cfg.EnsurePathsExist(); err != nil {
		return Offer{}, err
	}
	up, err := updater.New(cfg)
	if err != nil {
		return Offer{}, fmt.Errorf("update: trusted root: %w", err)
	}
	if err := up.Refresh(); err != nil {
		return Offer{}, fmt.Errorf("update: metadata: %w", err)
	}
	tf, err := up.GetTargetInfo(BundleTarget(platform))
	if err != nil {
		return Offer{}, fmt.Errorf("%w: %s", ErrNoBundle, platform)
	}
	return offerOf(tf)
}

// trustedRoot is the newest root this Hive has verified: the one it shipped
// with, or a later one go-tuf stored after following a rotation. Starting
// from the shipped root every time would let rotated-out keys sign a new
// "next" root (security review); TUF clients must keep the latest.
func trustedRoot(shipped []byte, cacheDir string) []byte {
	cached, err := os.ReadFile(filepath.Join(cacheDir, "root.json")) //nolint:gosec // the updater's private directory
	if err != nil {
		return shipped
	}
	a, err1 := (&metadata.Metadata[metadata.RootType]{}).FromBytes(shipped)
	b, err2 := (&metadata.Metadata[metadata.RootType]{}).FromBytes(cached)
	if err1 != nil || err2 != nil || b.Signed.Version <= a.Signed.Version {
		return shipped
	}
	return cached
}

func offerOf(tf *metadata.TargetFiles) (Offer, error) {
	var c bundleCustom
	if tf.Custom == nil || json.Unmarshal(*tf.Custom, &c) != nil || c.URL == "" {
		return Offer{}, fmt.Errorf("update: target %s has no bundle fields", tf.Path)
	}
	v, err := ParseVersion(c.Version)
	if err != nil {
		return Offer{}, err
	}
	o := Offer{URL: c.URL, file: tf, Target: Target{Version: v, Security: c.Security, Rollout: c.Rollout}}
	if c.MinUpdater != "" {
		if o.Target.MinUpdater, err = ParseVersion(c.MinUpdater); err != nil {
			return Offer{}, err
		}
	}
	return o, nil
}

// Download fetches the bundle, checks its length and hashes against the
// signed target, and unpacks the bundle files into dst.
func (o Offer) Download(ctx context.Context, client *http.Client, dst string) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: download %s: http %d", o.URL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, o.file.Length+1))
	if err != nil {
		return err
	}
	if err := o.file.VerifyLengthHashes(data); err != nil {
		return fmt.Errorf("update: bundle does not match its signature: %w", err)
	}
	return unpack(data, dst)
}

// unpack writes the bundle files found anywhere in a tar.gz or zip archive
// into dst, by base name only; it fails when one is missing.
func unpack(data []byte, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	found := map[string]bool{}
	keep := func(name string, r io.Reader) error {
		base := path.Base(filepath.ToSlash(name))
		if !slices.Contains(BundleFiles, base) || found[base] {
			return nil
		}
		found[base] = true
		return writeExecutable(filepath.Join(dst, base), r)
	}
	var err error
	if bytes.HasPrefix(data, []byte("PK")) {
		err = unzip(data, keep)
	} else {
		err = untar(data, keep)
	}
	if err != nil {
		return err
	}
	for _, n := range BundleFiles {
		if !found[n] {
			return fmt.Errorf("update: bundle lacks %s", n)
		}
	}
	return nil
}

func untar(data []byte, keep func(string, io.Reader) error) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg {
			if err := keep(h.Name, tr); err != nil {
				return err
			}
		}
	}
}

func unzip(data []byte, keep func(string, io.Reader) error) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = keep(f.Name, rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeExecutable(p string, r io.Reader) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // bundle executables
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(r, 512<<20)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
