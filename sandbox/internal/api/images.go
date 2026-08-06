package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The image allowlist.
//
// A stage does not choose a container image — it names a POLICY, and the
// controller looks up everything else here: which image reference to run, which
// network posture it gets, what it may spend, and which scratch paths it needs.
// The client never sends an image reference, a network name, a memory cap or a
// mount. That inversion is what keeps "the image is data, the hardening is code"
// true even once users can bring their own images: nothing a caller supplies can
// reach docker.RunArgs, whose flag vector stays identical for every image (see
// docker.TestRunArgs_HardeningIsIndependentOfTheImage).
//
// Bringing your own image is therefore a supply-chain question, not an isolation
// one. A hostile image still runs read-only, capability-less, with only the
// task's worktree mounted and no route off the egress island — it can waste CPU
// and corrupt /work, and nothing else. What the allowlist adds is *accountability*:
// the reference is resolved host-side to an immutable digest before launch, so a
// run records the bytes that actually executed.
//
// The unattended flag is the second axis. It splits images by whether a human is
// watching at run time, not by which screen launched the run:
//
//   - Daily is scheduled and unattended — only policies marked Unattended may
//     run there, so a schedule can never silently start executing an image
//     someone added while debugging.
//   - Delivery is attended (a reviewer is in the drawer) — any policy may run.
//
// Promoting a custom image to unattended is therefore an explicit, separate act
// in Settings rather than a side effect of using it once.

// Network postures a policy may select. Anything else is rejected: a policy is
// allowed to be MORE restrictive than the run's isolation, never less.
const (
	// NetworkEgress follows the run's isolation mode (strict => the internal
	// egress island where only the gateway and the registry proxy live).
	NetworkEgress = "egress"
	// NetworkNone attaches no network at all. A media stage transcodes untrusted
	// input and has no reason to reach even the gateway.
	NetworkNone = "none"
)

// DefaultImageName is the policy used when a stage names none.
const DefaultImageName = "base"

// ImagePolicy is one entry of the allowlist.
type ImagePolicy struct {
	// Name is what a stage asks for, e.g. "poly".
	Name string `json:"name"`
	// Ref is the image reference to run. Resolved to an immutable digest
	// host-side before launch.
	Ref string `json:"ref"`
	// Description is operator-facing text shown in Settings.
	Description string `json:"description"`
	// Network is the posture: "egress" (default) or "none".
	Network string `json:"network,omitempty"`
	// MemoryMB, CPUs, PidsLimit override the controller defaults. Zero => the
	// controller default. All are clamped to the configured ceilings.
	MemoryMB  int     `json:"memoryMB,omitempty"`
	CPUs      float64 `json:"cpus,omitempty"`
	PidsLimit int     `json:"pidsLimit,omitempty"`
	// Tmpfs are the writable scratch paths this image's toolchain needs on top
	// of the read-only root.
	Tmpfs []string `json:"tmpfs,omitempty"`
	// Unattended permits this image on scheduled (Daily) runs, where nobody is
	// watching. Shipped images are marked here; a custom one must be promoted.
	Unattended bool `json:"unattended,omitempty"`
	// Custom marks a policy added at runtime from Settings (as opposed to one
	// shipped in the config). Set by the controller, not by the client.
	Custom bool `json:"custom,omitempty"`
}

// Ceilings a policy may not exceed, applied when the config omits them.
const (
	defaultMaxMemoryMB  = 16384
	defaultMaxCPUs      = 8.0
	defaultMaxPidsLimit = 4096
)

func (c *Config) maxMemoryMB() int {
	if c.MaxMemoryMB > 0 {
		return c.MaxMemoryMB
	}
	return defaultMaxMemoryMB
}

func (c *Config) maxCPUs() float64 {
	if c.MaxCPUs > 0 {
		return c.MaxCPUs
	}
	return defaultMaxCPUs
}

func (c *Config) maxPidsLimit() int {
	if c.MaxPidsLimit > 0 {
		return c.MaxPidsLimit
	}
	return defaultMaxPidsLimit
}

// validImageName bounds a policy name to something safe to use in a container
// name and unambiguous in the UI.
func validImageName(name string) error {
	if name == "" {
		return fmt.Errorf("image name is required")
	}
	if len(name) > 32 {
		return fmt.Errorf("image name %q is too long (max 32)", name)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '-' || r == '_') && i > 0:
		default:
			return fmt.Errorf("image name %q must be lowercase letters, digits, - or _", name)
		}
	}
	return nil
}

// validImageRef rejects references that could be mistaken for a docker flag or
// smuggle a second argument. The reference is a bare argv element, never a
// shell string, so this is about argument confusion rather than injection.
func validImageRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("image ref is required")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("image ref %q must not start with '-'", ref)
	}
	if strings.ContainsAny(ref, " \t\n\r\v\f") {
		return fmt.Errorf("image ref %q must not contain whitespace", ref)
	}
	return nil
}

// validTmpfs bounds the scratch paths a policy may request. A tmpfs over /work
// would shadow the task's worktree, which is not an escape but does turn a run
// into a silent no-op — a confusing failure worth refusing outright.
func validTmpfs(paths []string) error {
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("tmpfs path %q must be absolute", p)
		}
		clean := filepath.Clean(p)
		if clean != p {
			return fmt.Errorf("tmpfs path %q must be in canonical form (%q)", p, clean)
		}
		if clean == "/" {
			return fmt.Errorf("tmpfs path %q would shadow the whole filesystem", p)
		}
		if clean == "/work" || strings.HasPrefix(clean, "/work/") {
			return fmt.Errorf("tmpfs path %q would shadow the mounted worktree", p)
		}
	}
	return nil
}

// normalize validates a policy and clamps it to the configured ceilings.
func (p ImagePolicy) normalize(cfg *Config) (ImagePolicy, error) {
	if err := validImageName(p.Name); err != nil {
		return p, err
	}
	if err := validImageRef(p.Ref); err != nil {
		return p, err
	}
	switch p.Network {
	case "", NetworkEgress:
		p.Network = NetworkEgress
	case NetworkNone:
	default:
		return p, fmt.Errorf("image %q: network must be %q or %q (a policy may only be more restrictive than the run)", p.Name, NetworkEgress, NetworkNone)
	}
	if err := validTmpfs(p.Tmpfs); err != nil {
		return p, fmt.Errorf("image %q: %w", p.Name, err)
	}
	if p.MemoryMB < 0 || p.CPUs < 0 || p.PidsLimit < 0 {
		return p, fmt.Errorf("image %q: resource limits must not be negative", p.Name)
	}
	if p.MemoryMB > cfg.maxMemoryMB() {
		p.MemoryMB = cfg.maxMemoryMB()
	}
	if p.CPUs > cfg.maxCPUs() {
		p.CPUs = cfg.maxCPUs()
	}
	if p.PidsLimit > cfg.maxPidsLimit() {
		p.PidsLimit = cfg.maxPidsLimit()
	}
	return p, nil
}

// imagesPath is where runtime-added (Settings) policies are persisted. Like the
// retention override, it lives in the archive root rather than being written
// back into the config file, which ships as a bundle resource.
func (s *Server) imagesPath() string {
	return filepath.Join(s.cfg.logDir(), "images.json")
}

// customImages returns the persisted runtime policies (empty on any read or
// parse failure — a corrupt file must not take the controller down).
func (s *Server) customImages() []ImagePolicy {
	raw, err := os.ReadFile(s.imagesPath())
	if err != nil {
		return nil
	}
	var list []ImagePolicy
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := make([]ImagePolicy, 0, len(list))
	for _, p := range list {
		p.Custom = true
		if norm, err := p.normalize(s.cfg); err == nil {
			out = append(out, norm)
		}
	}
	return out
}

func (s *Server) saveCustomImages(list []ImagePolicy) error {
	if err := os.MkdirAll(s.cfg.logDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.imagesPath(), raw, 0o600)
}

// configImages returns the shipped policies. When the config declares none, the
// legacy single `image` field becomes the one "base" policy, so an install that
// predates the allowlist keeps running unchanged.
func (s *Server) configImages() []ImagePolicy {
	if len(s.cfg.Images) > 0 {
		out := make([]ImagePolicy, 0, len(s.cfg.Images))
		for _, p := range s.cfg.Images {
			if norm, err := p.normalize(s.cfg); err == nil {
				out = append(out, norm)
			}
		}
		return out
	}
	ref := s.cfg.Image
	if ref == "" {
		return nil
	}
	return []ImagePolicy{{
		Name:        DefaultImageName,
		Ref:         ref,
		Description: "controller default",
		Network:     NetworkEgress,
		Unattended:  true,
	}}
}

// images returns the effective allowlist: shipped policies first, then custom
// ones, sorted by name. A custom policy never shadows a shipped one (upsert
// refuses the name collision), so the merge cannot silently redefine "base".
func (s *Server) images() []ImagePolicy {
	out := append(s.configImages(), s.customImages()...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// resolveImage picks the policy a stage will run under.
//
// An unknown name is an error rather than a fallback to the default: silently
// substituting a different image is exactly the kind of surprise the allowlist
// exists to prevent.
func (s *Server) resolveImage(name string, unattended bool) (ImagePolicy, error) {
	list := s.images()
	if len(list) == 0 {
		return ImagePolicy{}, fmt.Errorf("no container images are configured")
	}
	if name == "" {
		name = DefaultImageName
	}
	for _, p := range list {
		if p.Name != name {
			continue
		}
		if unattended && !p.Unattended {
			return ImagePolicy{}, fmt.Errorf("image %q is not approved for scheduled runs; promote it in Settings → サンドボックス first", name)
		}
		return p, nil
	}
	if name == DefaultImageName {
		// No policy literally called "base": fall back to the first shipped one
		// so a config that names its images differently still works.
		for _, p := range list {
			if !p.Custom && (!unattended || p.Unattended) {
				return p, nil
			}
		}
	}
	return ImagePolicy{}, fmt.Errorf("unknown image %q (allowed: %s)", name, strings.Join(imageNames(list), ", "))
}

func imageNames(list []ImagePolicy) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.Name)
	}
	return out
}

func (s *Server) handleImagesGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"images":       s.images(),
		"default":      DefaultImageName,
		"maxMemoryMB":  s.cfg.maxMemoryMB(),
		"maxCPUs":      s.cfg.maxCPUs(),
		"maxPidsLimit": s.cfg.maxPidsLimit(),
	})
}

// handleImagesSet upserts a custom policy. Shipped policies are not editable
// here — they are part of the install, and letting Settings rewrite "base" would
// make the shipped baseline a moving target.
func (s *Server) handleImagesSet(w http.ResponseWriter, r *http.Request) {
	var req ImagePolicy
	if !decode(w, r, &req) {
		return
	}
	req.Custom = true
	policy, err := req.normalize(s.cfg)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	for _, p := range s.configImages() {
		if p.Name == policy.Name {
			writeErr(w, 400, fmt.Sprintf("%q is a built-in image and cannot be redefined; choose another name", policy.Name))
			return
		}
	}
	list := s.customImages()
	replaced := false
	for i := range list {
		if list[i].Name == policy.Name {
			list[i] = policy
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, policy)
	}
	if err := s.saveCustomImages(list); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"images": s.images()})
}

func (s *Server) handleImagesDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	list := s.customImages()
	kept := make([]ImagePolicy, 0, len(list))
	for _, p := range list {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(list) {
		writeErr(w, 404, fmt.Sprintf("no custom image named %q (built-in images cannot be removed)", name))
		return
	}
	if err := s.saveCustomImages(kept); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"images": s.images()})
}
