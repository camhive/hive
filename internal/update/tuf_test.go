package update_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/camhive/hive/internal/update"
)

// roles holds the four top-level metadata documents of a repository.
type roles struct {
	root      *metadata.Metadata[metadata.RootType]
	targets   *metadata.Metadata[metadata.TargetsType]
	snapshot  *metadata.Metadata[metadata.SnapshotType]
	timestamp *metadata.Metadata[metadata.TimestampType]
}

func (r *roles) Root() *metadata.Metadata[metadata.RootType]             { return r.root }
func (r *roles) Targets(string) *metadata.Metadata[metadata.TargetsType] { return r.targets }
func (r *roles) Snapshot() *metadata.Metadata[metadata.SnapshotType]     { return r.snapshot }
func (r *roles) Timestamp() *metadata.Metadata[metadata.TimestampType]   { return r.timestamp }

// tufRepo is a signed channel served over HTTP: metadata under /meta,
// bundles under /dl (standing in for GitHub Releases).
type tufRepo struct {
	srv     *httptest.Server
	dir     string
	roles   *roles
	keys    map[string]ed25519.PrivateKey
	bundles map[string][]byte
}

func newRepo(t *testing.T) *tufRepo {
	t.Helper()
	exp := time.Now().Add(24 * time.Hour)
	r := &tufRepo{dir: t.TempDir(), keys: map[string]ed25519.PrivateKey{}, bundles: map[string][]byte{},
		roles: &roles{root: metadata.Root(exp), targets: metadata.Targets(exp), snapshot: metadata.Snapshot(exp), timestamp: metadata.Timestamp(exp)}}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		r.keys[role] = priv
		key, err := metadata.KeyFromPublicKey(priv.Public())
		if err != nil {
			t.Fatal(err)
		}
		if err := r.roles.Root().Signed.AddKey(key, role); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/meta/", http.StripPrefix("/meta/", http.FileServer(http.Dir(r.dir))))
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, req *http.Request) {
		b, ok := r.bundles[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	r.publish(t, "root")
	return r
}

func (r *tufRepo) sign(t *testing.T, role string, sign func(signature.Signer) error) {
	t.Helper()
	s, err := signature.LoadSigner(r.keys[role], crypto.Hash(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := sign(s); err != nil {
		t.Fatal(err)
	}
}

// publish signs and writes the given roles (and the chain above them).
func (r *tufRepo) publish(t *testing.T, roles ...string) {
	t.Helper()
	for _, role := range roles {
		switch role {
		case "root":
			r.roles.Root().Signatures = nil
			r.sign(t, "root", func(s signature.Signer) error { _, err := r.roles.Root().Sign(s); return err })
			if err := r.roles.Root().ToFile(filepath.Join(r.dir, fmt.Sprintf("%d.root.json", r.roles.Root().Signed.Version)), false); err != nil {
				t.Fatal(err)
			}
		case "targets":
			r.roles.Targets("targets").Signatures = nil
			r.sign(t, "targets", func(s signature.Signer) error { _, err := r.roles.Targets("targets").Sign(s); return err })
			if err := r.roles.Targets("targets").ToFile(filepath.Join(r.dir, fmt.Sprintf("%d.targets.json", r.roles.Targets("targets").Signed.Version)), false); err != nil {
				t.Fatal(err)
			}
			r.roles.Snapshot().Signed.Meta["targets.json"] = metadata.MetaFile(r.roles.Targets("targets").Signed.Version)
			r.roles.Snapshot().Signed.Version++
			r.roles.Snapshot().Signatures = nil
			r.sign(t, "snapshot", func(s signature.Signer) error { _, err := r.roles.Snapshot().Sign(s); return err })
			if err := r.roles.Snapshot().ToFile(filepath.Join(r.dir, fmt.Sprintf("%d.snapshot.json", r.roles.Snapshot().Signed.Version)), false); err != nil {
				t.Fatal(err)
			}
			r.roles.Timestamp().Signed.Meta["snapshot.json"] = metadata.MetaFile(r.roles.Snapshot().Signed.Version)
			r.publish(t, "timestamp")
		case "timestamp":
			r.roles.Timestamp().Signed.Version++
			r.roles.Timestamp().Signatures = nil
			r.sign(t, "timestamp", func(s signature.Signer) error { _, err := r.roles.Timestamp().Sign(s); return err })
			if err := r.roles.Timestamp().ToFile(filepath.Join(r.dir, "timestamp.json"), false); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// release publishes a bundle for a platform with the given custom fields.
func (r *tufRepo) release(t *testing.T, platform, version string, data []byte, custom map[string]any) { //nolint:unparam // one platform is enough so far
	t.Helper()
	path := fmt.Sprintf("/dl/v%s/hive_%s_%s.tar.gz", version, version, platform)
	r.bundles[path] = data
	tf, err := metadata.TargetFile().FromBytes("bundles/"+platform, data, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	c := map[string]any{"version": version, "url": r.srv.URL + path, "rollout": 100}
	for k, v := range custom {
		c[k] = v
	}
	raw, _ := json.Marshal(c)
	msg := json.RawMessage(raw)
	tf.Custom = &msg
	targets := r.roles.Targets("targets")
	targets.Signed.Targets["bundles/"+platform] = tf
	targets.Signed.Version++
	r.publish(t, "targets")
}

func (r *tufRepo) rootJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, "1.root.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (r *tufRepo) source(t *testing.T) update.Source {
	return update.Source{MetadataURL: r.srv.URL + "/meta", Root: r.rootJSON(t), CacheDir: t.TempDir()}
}

// tarGz packs the bundle files under a top-level directory, as GoReleaser does.
func tarGz(t *testing.T, tag string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range append(append([]string{}, update.BundleFiles...), "LICENSE") {
		body := []byte(tag + " " + n)
		if err := tw.WriteHeader(&tar.Header{Name: "hive_x/" + n, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestSignedBundleIsOfferedDownloadedVerifiedAndUnpacked(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	r.release(t, "linux_arm64", "0.1.5", tarGz(t, "v5"), map[string]any{"security": true, "min_updater": "0.1.4"})
	offer, err := r.source(t).Offer(ctx, "linux_arm64")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Target.Version.String() != "0.1.5" || !offer.Target.Security || offer.Target.Rollout != 100 || offer.Target.MinUpdater.String() != "0.1.4" {
		t.Fatalf("offer = %+v", offer.Target)
	}
	dst := t.TempDir()
	if err := offer.Download(ctx, nil, dst); err != nil {
		t.Fatal(err)
	}
	for _, n := range update.BundleFiles {
		if b, err := os.ReadFile(filepath.Join(dst, n)); err != nil || string(b) != "v5 "+n {
			t.Fatalf("%s: %q %v", n, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "LICENSE")); err == nil {
		t.Fatal("unpacked more than the bundle files")
	}
	if _, err := r.source(t).Offer(ctx, "windows_amd64"); !errors.Is(err, update.ErrNoBundle) {
		t.Fatalf("missing platform: %v", err)
	}
}

// The transports are untrusted: a swapped bundle, a foreign root and stale
// metadata are all refused.
func TestTamperedBundleForeignRootAndStaleMetadataAreRefused(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	r.release(t, "linux_arm64", "0.1.5", tarGz(t, "good"), nil)
	offer, err := r.source(t).Offer(ctx, "linux_arm64")
	if err != nil {
		t.Fatal(err)
	}
	for p := range r.bundles {
		r.bundles[p] = tarGz(t, "evil") // the download host serves something else
	}
	if err := offer.Download(ctx, nil, t.TempDir()); err == nil {
		t.Fatal("a tampered bundle was accepted")
	}

	other := newRepo(t)
	src := r.source(t)
	src.Root = other.rootJSON(t)
	if _, err := src.Offer(ctx, "linux_arm64"); err == nil {
		t.Fatal("metadata accepted under a foreign root")
	}

	r.roles.Timestamp().Signed.Expires = time.Now().Add(-time.Minute) // the online key stopped re-signing
	r.publish(t, "timestamp")
	if _, err := r.source(t).Offer(ctx, "linux_arm64"); err == nil {
		t.Fatal("stale metadata accepted")
	}
}

func TestBundleWithPathTricksOrMissingFilesIsRefused(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../../hive", Mode: 0o755, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	r.release(t, "linux_arm64", "0.1.5", buf.Bytes(), nil)
	offer, err := r.source(t).Offer(ctx, "linux_arm64")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "in")
	if err := offer.Download(ctx, nil, dst); err == nil {
		t.Fatal("a bundle without its files was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "hive")); err == nil {
		t.Fatal("wrote outside the destination")
	}
}
