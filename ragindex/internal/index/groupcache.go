package index

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// Keeping group membership across a restart.
//
// Membership is not derived state. The vectors beside it are — sources are the
// truth and rebuilding from them is always correct — but which group a source
// belongs to exists only in the host's Knowledge graph, and this process cannot
// ask for it: the host agent is off the container network by design. It is told,
// and if it forgets it has no way to find out again.
//
// Forgetting is not hypothetical. Registering or removing a knowledge source
// rebinds a mount, which restarts this container; so does restarting the app.
// Before this file, every one of those dropped the labels until someone next
// opened the Knowledge screen, which is the only thing that pushed them.
//
// What that costs is worth being exact about. Since a source's scope is read
// off its membership — see permits — an untagged source is one nobody has
// claimed, which would otherwise read as everyone's: a lost mapping would fail
// OPEN and a scoped run would retrieve the whole index. closeUnclaimedLocked
// refuses to let it, so what a lost mapping actually causes is an outage rather
// than a leak. That is the right way round, and still worth not having: this
// file is what keeps a restart from causing either.
//
// The file is a plain JSON object and is rewritten whole on every push, because
// that is what a push is: the complete mapping as the host currently knows it,
// never a delta. A damaged or missing file is not an error — it leaves the
// indexer in exactly the state it was in before this existed, waiting to be
// told — but it is logged, because that state is no longer a safe one to sit in
// quietly.

// groupCacheFile is the name inside the configured cache directory.
const groupCacheFile = "groups.json"

// groupCachePath returns where the mapping lives, or "" when no cache directory
// is configured — in which case membership stays in memory, as it always did.
func (i *Index) groupCachePath() string {
	if i.cfg.CacheDir == "" {
		return ""
	}
	return filepath.Join(i.cfg.CacheDir, groupCacheFile)
}

// LoadGroups restores the mapping pushed before the last restart.
//
// It only seeds the mapping; it does not tag anything, because at startup there
// is nothing to tag yet. The first build applies it — swap re-applies membership
// after every rebuild for exactly this reason — so the labels are in place
// before the index is first searchable.
func (i *Index) LoadGroups() {
	path := i.groupCachePath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("ragindex: reading group cache: %v", err)
		}
		return
	}
	var m map[string][]string
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Printf("ragindex: ignoring damaged group cache: %v", err)
		return
	}
	if m == nil {
		return
	}
	i.membership.set(m)
	log.Printf("ragindex: restored group membership for %d source(s)", len(m))
}

// saveGroups writes the mapping so the next start does not begin blind.
//
// Written to a neighbouring temp file and renamed, so a crash mid-write leaves
// the previous mapping rather than a truncated one. A half-read file would
// deserialise to a smaller set of labels, and fewer labels is precisely the
// failure this is here to prevent.
func (i *Index) saveGroups(m map[string][]string) {
	path := i.groupCachePath()
	if path == "" {
		return
	}
	raw, err := json.Marshal(m)
	if err != nil {
		log.Printf("ragindex: encoding group cache: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("ragindex: writing group cache: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("ragindex: replacing group cache: %v", err)
		os.Remove(tmp)
	}
}
