package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"orchestra/sandbox/internal/docker"
	"orchestra/sandbox/internal/worktree"
)

// This file implements Orchestra's multi-agent orchestrator. It is deliberately
// template-agnostic: the only primitive is a DAG of Stages, each of which runs
// as one hardened sandbox. A "Graph template" (or a Solo, or any future shape)
// is compiled by the caller into a Stage DAG, so the executor stays a generic
// scheduler and both the orchestrator and the graphs it runs are freely
// configurable.
//
// A Stage becomes ready once all of its DependsOn stages have completed
// successfully; ready stages run up to MaxParallel at a time; a stage whose
// dependency failed/was skipped is itself skipped. Stages hand work off through
// the shared git worktree mounted into every sandbox.

// Stage is one node of the run DAG (as submitted by the client).
type Stage struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Model string `json:"model"` // ORCHESTRA_MODEL (optional)
	// Provider is the LLM dialect (anthropic|openai|gemini) → ORCHESTRA_PROVIDER;
	// ProviderPrefix is the gateway route for it (e.g. "/openai/"). Together they
	// point the agent at the right gateway provider. Empty => default (anthropic).
	Provider       string `json:"provider"`
	ProviderPrefix string `json:"providerPrefix"`
	System         string `json:"system"` // ORCHESTRA_SYSTEM (role prompt)
	Task           string `json:"task"`   // ORCHESTRA_TASK (optional per-stage override)
	// Effort tunes reasoning depth / token spend (low|medium|high|xhigh|max) →
	// ORCHESTRA_EFFORT; MaxTokens caps per-response output → ORCHESTRA_MAX_TOKENS.
	// Both are cost controls; empty/0 => the agent's own defaults apply.
	Effort    string            `json:"effort"`
	MaxTokens int               `json:"maxTokens"`
	DependsOn []string          `json:"dependsOn"`
	Env       map[string]string `json:"env"`
	// Image names an entry of the controller's image ALLOWLIST (see images.go) —
	// "base", "poly", "media", or a custom one added from Settings. It is a
	// policy name, never an image reference: the controller looks up the ref,
	// the network posture, the resource caps and the scratch mounts. Empty =>
	// the default policy.
	Image string `json:"image"`
	// Cmd replaces the image's default command. It is how a stage runs a build,
	// a test suite or a transcode instead of an LLM agent loop — the toolchain
	// images ship a shell for exactly this. The command runs under the same
	// hardening as every other sandbox (read-only root, no capabilities, only
	// /work mounted, no route off the egress island), so this is a change in
	// what a stage *does*, not in what it can reach. Empty => the image default.
	Cmd []string `json:"cmd"`
	// Tools are custom HTTP tools (through the gateway) exposed to this stage's
	// agent; forwarded verbatim as ORCHESTRA_TOOLS.
	//
	// Held as raw objects rather than a struct for the same reason as Media and
	// Web: what a tool consists of is the agent's business and the template's,
	// not the controller's. A struct here does not merely fail to understand a
	// new field — it deletes it. That is what happened to the artifact output
	// binding: the controller re-marshalled every tool through a shape that
	// predated it, so the agent received image generation as an ordinary text
	// tool, called it on a 30-second client, and got a timeout instead of a
	// picture.
	Tools []map[string]any `json:"tools"`
	// Media enables the generation tools (image / speech / video) for this
	// stage, forwarded verbatim as ORCHESTRA_MEDIA. Passed through opaquely for
	// the same reason as Tools: what a provider's route is called is the
	// gateway's business and the template's, not the controller's. Absent =>
	// the stage cannot generate media at all.
	Media map[string]any `json:"media,omitempty"`
	// Groups is this stage's knowledge scope, already resolved and widened by
	// whoever owns the Knowledge graph. Absent falls back to the run's scope;
	// present means this stage authenticates with a session of its own, because
	// how far a stage may follow relations is a property of its agent template
	// rather than of the run.
	Groups []string `json:"groups,omitempty"`
	// Web grants this stage the provider-side web_search tool, forwarded
	// verbatim as ORCHESTRA_WEB_SEARCH. Unlike Media it opens no route out of
	// the container — the model provider runs the search and returns the
	// results inside the same /v1/messages response — but it is still a
	// per-stage grant, because searches are billed per use and because an agent
	// that was not given the tool must not be able to reach for it.
	Web map[string]any `json:"web,omitempty"`
}

type runReq struct {
	TaskID       string `json:"taskId"`
	WorktreePath string `json:"worktreePath"`
	Isolation    string `json:"isolation"`
	// WorktreeMode selects how stages share files. "shared" (default) mounts one
	// worktree into every stage (stages hand off through it). "isolated" gives
	// each stage its own git worktree seeded from its dependencies' output, so
	// parallel stages never clobber each other; sinks are merged back at the end.
	WorktreeMode string `json:"worktreeMode"`
	// Delegation enables runtime supervisor delegation: each stage agent gets the
	// spawn_subagent tool (file-based, no network to the host) and the controller
	// runs the requested sub-agents in the stage's worktree.
	Delegation bool `json:"delegation"`
	// Unattended marks a run that nobody is watching — a Daily schedule firing,
	// as opposed to a reviewer starting a run from the Delivery drawer. It
	// restricts the run to images explicitly approved for unattended use, so a
	// schedule can never silently start executing an image someone added while
	// debugging. It only ever narrows what is permitted.
	Unattended bool `json:"unattended"`
	// Groups is the run's knowledge scope: the groups its agents may retrieve
	// through the gateway's /rag route. nil means no scope was asked for, so the
	// run falls back to the shared session — which states no entitlement and is
	// refused the indexer; an empty slice is the global scope, a run entitled to
	// exactly the knowledge declared as everyone's. Both retrieve far less than
	// everything, and they are not the same, so the distinction is carried all
	// the way to the gateway and must not be collapsed here.
	Groups        []string `json:"groups"`
	MaxParallel   int      `json:"maxParallel"`
	StopOnFailure *bool    `json:"stopOnFailure"`
	Stages        []Stage  `json:"stages"`
}

// Stage status values.
const (
	statusPending = "pending"
	statusRunning = "running"
	statusDone    = "done"
	statusFailed  = "failed"
	statusSkipped = "skipped"
	statusStopped = "stopped"
)

// StageState is the live state of one stage (serialized to clients). DependsOn
// is echoed so the UI can render the DAG from the run status alone.
type StageState struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	DependsOn   []string `json:"dependsOn"`
	ContainerID string   `json:"containerId"`
	Status      string   `json:"status"`
	ExitCode    int      `json:"exitCode"`
	// Error is why the stage failed before (or instead of) producing an exit
	// code — an image that would not resolve, a container that would not start,
	// a worktree that could not be prepared. Those failures leave no container,
	// so there is no log to read: without this the whole run reports nothing but
	// exitCode -1, and the reason has to be reconstructed from what is missing.
	Error string `json:"error,omitempty"`
	// Image is the allowlist policy this stage ran under; ImageDigest is the
	// immutable id that policy's reference resolved to at launch. A tag moves,
	// so the digest is what makes "which bytes actually ran" answerable after
	// the fact — and it is the only image field worth trusting in an archive.
	Image       string `json:"image,omitempty"`
	ImageDigest string `json:"imageDigest,omitempty"`
	// What the stage produced: the commit recording its output, the commit it
	// built on, and the files it touched. Empty when the stage changed nothing
	// (a read-only planner, say) or when the worktree could not be recorded.
	Commit string                `json:"commit,omitempty"`
	Parent string                `json:"parent,omitempty"`
	Files  []worktree.FileChange `json:"files,omitempty"`
}

// Run is an in-flight (or finished) orchestrated run. Exported fields are
// serialized; the mutex/index/flags are internal.
type Run struct {
	ID          string        `json:"id"`
	TaskID      string        `json:"taskId"`
	Status      string        `json:"status"` // running/done/failed/stopped
	MaxParallel int           `json:"maxParallel"`
	Stages      []*StageState `json:"stages"`
	// Artifacts are the worktree-relative files the run's stages wrote, as they
	// reported them. Set once the run is terminal. Empty on a run that produced
	// nothing — which is a different fact from a run that failed, and one the
	// old status word could not express: every stage could exit 0 and leave the
	// output directory empty.
	Artifacts []string `json:"artifacts"`

	mu          sync.Mutex
	stageByID   map[string]*StageState
	stageCommit map[string]string // stageID -> result commit (isolated mode)
	stopping    bool              // user-requested stop

	// policies is the image policy each stage resolved to, decided once at
	// admission so a run cannot start and then fail at stage 7 on a bad image.
	policies map[string]ImagePolicy

	// session is the gateway session this run's stages authenticate with. Set
	// only when the run asked for a knowledge scope: the scope lives on the
	// session because the gateway will not accept one from the caller.
	session string
	// stageSessions holds the extra sessions minted for stages whose scope
	// differs from the run's, keyed by stage id. Every one of them is revoked
	// when the run ends.
	stageSessions map[string]string
	delegation    bool // stages may spawn sub-agents (runtime delegation)
	maxDepth      int  // delegation depth cap
}

// validateStages checks the DAG is well-formed: non-empty, unique ids, deps that
// reference existing stages, and no cycle (Kahn's algorithm).
func validateStages(stages []Stage) error {
	if len(stages) == 0 {
		return fmt.Errorf("run requires at least one stage")
	}
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, st := range stages {
		if st.ID == "" {
			return fmt.Errorf("every stage requires an id")
		}
		if _, dup := indeg[st.ID]; dup {
			return fmt.Errorf("duplicate stage id %q", st.ID)
		}
		indeg[st.ID] = 0
	}
	for _, st := range stages {
		for _, dep := range st.DependsOn {
			if _, ok := indeg[dep]; !ok {
				return fmt.Errorf("stage %q depends on unknown stage %q", st.ID, dep)
			}
			adj[dep] = append(adj[dep], st.ID)
			indeg[st.ID]++
		}
	}
	// Kahn: peel zero-indegree nodes; if any remain, there's a cycle.
	queue := []string{}
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	seen := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		seen++
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if seen != len(stages) {
		return fmt.Errorf("stage graph has a cycle")
	}
	return nil
}

func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	var req runReq
	if !decode(w, r, &req) {
		return
	}
	if req.TaskID == "" {
		writeErr(w, 400, "taskId is required")
		return
	}
	if req.WorktreePath == "" {
		writeErr(w, 400, "worktreePath is required")
		return
	}
	if err := validateStages(req.Stages); err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	maxPar := req.MaxParallel
	if maxPar < 1 {
		maxPar = 1
	}
	stopOnFailure := true
	if req.StopOnFailure != nil {
		stopOnFailure = *req.StopOnFailure
	}
	strict := !strings.EqualFold(req.Isolation, "relaxed")

	// Resolve every stage's image against the allowlist before the run is
	// admitted. A typo'd name, or one that has not been promoted for unattended
	// use, should be a 400 here rather than a run that dies at stage 7 having
	// already produced half a deliverable.
	policies := map[string]ImagePolicy{}
	for _, st := range req.Stages {
		p, err := s.resolveImage(st.Image, req.Unattended)
		if err != nil {
			writeErr(w, 400, fmt.Sprintf("stage %q: %v", st.ID, err))
			return
		}
		policies[st.ID] = p
	}

	run := &Run{
		ID:          runID(),
		TaskID:      req.TaskID,
		Status:      statusRunning,
		MaxParallel: maxPar,
		stageByID:   map[string]*StageState{},
		stageCommit: map[string]string{},
		policies:    policies,
		delegation:  req.Delegation,
		maxDepth:    s.cfg.maxDelegateDepth(),
	}
	stages := map[string]Stage{}
	for _, st := range req.Stages {
		state := &StageState{ID: st.ID, Name: st.Name, Role: st.Role, DependsOn: st.DependsOn, Status: statusPending, Image: policies[st.ID].Name}
		run.Stages = append(run.Stages, state)
		run.stageByID[st.ID] = state
		stages[st.ID] = st
	}

	// A knowledge scope, if the run asked for one. Minted before anything
	// starts and refused loudly on failure: falling back to the shared session
	// would silently give the run every group, which is the opposite of what
	// asking for a scope means.
	if req.Groups != nil {
		if s.adminToken() == "" {
			writeErr(w, 400, "this run asks for a knowledge scope, but the controller has no gateway admin token to mint a session with")
			return
		}
		tok, err := s.mintRunSession(run.ID, req.Groups)
		if err != nil {
			writeErr(w, 502, "could not mint a scoped session: "+err.Error())
			return
		}
		run.session = tok
	}

	// Stages whose scope differs from the run's get a session of their own.
	// Minted once per distinct group set: a run where every stage follows the
	// same number of relation hops needs one session, not one per stage.
	if err := s.mintStageSessions(run, req.Stages); err != nil {
		s.revokeAllSessions(run)
		writeErr(w, 502, "could not mint a stage session: "+err.Error())
		return
	}

	// Isolated mode: one git worktree per stage. Falls back to shared if the base
	// path is not a git worktree (e.g. a plain directory), so runs never break.
	var mgr *worktree.Manager
	isolated := strings.EqualFold(req.WorktreeMode, "isolated")
	if isolated {
		if m, err := worktree.New(req.WorktreePath, run.ID); err != nil {
			log.Printf("sandbox: worktree isolation unavailable, using shared worktree: %v", err)
			isolated = false
		} else {
			mgr = m
		}
	}

	// Shared mode puts every stage in one worktree, so concurrent stages would
	// write over each other. That was only ever safe because callers happened to
	// send maxParallel=1; make it a stated precondition instead of a coincidence.
	if !isolated && maxPar > 1 {
		writeErr(w, 400, "worktreeMode \"shared\" requires maxParallel=1; use \"isolated\" to run stages concurrently")
		return
	}

	// Shared mode still needs stage boundaries recorded, or a run leaves one
	// undifferentiated pile of edits with nothing attributable to a stage.
	var rec *worktree.Recorder
	if !isolated {
		if r, err := worktree.NewRecorder(req.WorktreePath, run.ID); err != nil {
			log.Printf("sandbox: stage commits unavailable for %s: %v", run.ID, err)
		} else {
			rec = r
		}
	}

	s.rmu.Lock()
	s.runs[run.ID] = run
	s.rmu.Unlock()

	go s.executeRun(run, stages, req.WorktreePath, strict, stopOnFailure, mgr, rec)

	writeJSON(w, 201, map[string]string{"runId": run.ID})
}

// stageResult carries a finished stage back to the scheduler.
type stageResult struct {
	id          string
	containerID string
	code        int
	err         error
}

// executeRun is the generic DAG scheduler. It runs in its own goroutine; all
// StageState mutations happen here (under run.mu), while per-stage goroutines
// only touch Docker and report back over the completions channel.
func (s *Server) executeRun(run *Run, stages map[string]Stage, worktree string, strict, stopOnFailure bool, mgr *worktree.Manager, rec *worktree.Recorder) {
	if mgr != nil {
		defer mgr.Cleanup()
	}
	completions := make(chan stageResult)
	running := 0
	halt := false // set when a failure should stop launching new stages

	for {
		run.mu.Lock()
		var ready []*StageState
		active := 0
		for _, st := range run.Stages {
			switch st.Status {
			case statusPending:
				if run.stopping || halt {
					st.Status = statusSkipped
					continue
				}
				switch depState(run, st) {
				case depSkip:
					st.Status = statusSkipped
				case depReady:
					ready = append(ready, st)
				}
			case statusRunning:
				active++
			}
		}
		for _, st := range ready {
			if running >= run.MaxParallel {
				break
			}
			st.Status = statusRunning
			running++
			active++
			go s.runStage(run, stages[st.ID], worktree, strict, completions, mgr, rec)
		}
		run.mu.Unlock()

		if active == 0 && running == 0 {
			break // everything is in a terminal state
		}

		res := <-completions
		running--
		run.mu.Lock()
		st := run.stageByID[res.id]
		st.ContainerID = res.containerID
		switch {
		case run.stopping:
			st.Status = statusStopped
		case res.err != nil:
			st.Status = statusFailed
			st.ExitCode = -1
			st.Error = res.err.Error()
			// Also to the controller's log: a stage that never got a container
			// has no container log, and this is often an operator-fixable
			// environment fault (docker missing, image gone) rather than
			// anything the agent did.
			log.Printf("sandbox: run %s stage %s failed to launch: %v", run.ID, res.id, res.err)
			if stopOnFailure {
				halt = true
			}
		case res.code != 0:
			st.Status = statusFailed
			st.ExitCode = res.code
			if stopOnFailure {
				halt = true
			}
		default:
			st.Status = statusDone
		}
		run.mu.Unlock()

		// Snapshot after every stage, so a controller that dies mid-run still
		// leaves the stages that had finished — including their commits.
		s.archiveRun(run)
	}

	// Compute the outcome, then — in isolated mode — land the run's sink stages
	// back on the base branch BEFORE declaring the run terminal, so a client that
	// observes "done" sees a fully integrated base worktree (not a half-merged one).
	run.mu.Lock()
	status := finalizeStatus(run)
	var commits []string
	if mgr != nil && status == statusDone {
		for _, id := range sinkStages(stages) {
			if st := run.stageByID[id]; st != nil && st.Status == statusDone {
				if c := run.stageCommit[id]; c != "" {
					commits = append(commits, c)
				}
			}
		}
	}
	run.mu.Unlock()

	if mgr != nil && status == statusDone {
		if err := mgr.Integrate(commits); err != nil {
			log.Printf("sandbox: run %s integrate failed: %v", run.ID, err)
			status = statusFailed
		}
	}

	// What the run made, from the stages' own manifests. Read before the status
	// is published so a client that sees a terminal status sees the artifacts
	// with it, rather than an empty list that fills in a moment later.
	artifacts := collectArtifacts(worktree)

	run.mu.Lock()
	run.Status = status
	run.Artifacts = artifacts
	run.mu.Unlock()

	if status == statusDone && len(artifacts) == 0 {
		// Worth saying out loud: nothing failed, so nothing else will mention
		// it, and an empty output directory is otherwise indistinguishable from
		// a run that was never launched.
		log.Printf("sandbox: run %s finished without producing any files", run.ID)
	}

	// The run's sessions outlive nothing: its containers are gone and their
	// scopes have no further use.
	s.revokeAllSessions(run)

	// Final snapshot: the terminal status and every stage's artifacts.
	s.archiveRun(run)
}

// sinkStages returns the ids of stages that no other stage depends on.
func sinkStages(stages map[string]Stage) []string {
	hasDependent := map[string]bool{}
	for _, st := range stages {
		for _, dep := range st.DependsOn {
			hasDependent[dep] = true
		}
	}
	var sinks []string
	for id := range stages {
		if !hasDependent[id] {
			sinks = append(sinks, id)
		}
	}
	return sinks
}

// runStage launches one sandbox for a stage and waits for it to exit. In
// isolated mode it first prepares a per-stage git worktree seeded from the
// stage's dependencies, and commits the stage's output on success.
func (s *Server) runStage(run *Run, stage Stage, worktree string, strict bool, out chan<- stageResult, mgr *worktree.Manager, rec *worktree.Recorder) {
	stageWorktree := worktree
	if mgr != nil {
		run.mu.Lock()
		parents := make([]string, 0, len(stage.DependsOn))
		for _, dep := range stage.DependsOn {
			if c := run.stageCommit[dep]; c != "" {
				parents = append(parents, c)
			}
		}
		run.mu.Unlock()
		dir, err := mgr.Prepare(stage.ID, parents)
		if err != nil {
			out <- stageResult{id: stage.ID, code: -1, err: err}
			return
		}
		stageWorktree = dir
	}

	env := map[string]string{}
	for k, v := range stage.Env {
		env[k] = v
	}
	if stage.System != "" {
		env["ORCHESTRA_SYSTEM"] = stage.System
	}
	if stage.Model != "" {
		env["ORCHESTRA_MODEL"] = stage.Model
	}
	if stage.Task != "" {
		env["ORCHESTRA_TASK"] = stage.Task
	}
	// Cost controls (optional per stage; the agent falls back to its defaults).
	if stage.Effort != "" {
		env["ORCHESTRA_EFFORT"] = stage.Effort
	}
	if stage.MaxTokens > 0 {
		env["ORCHESTRA_MAX_TOKENS"] = strconv.Itoa(stage.MaxTokens)
	}
	// Point the agent at the selected gateway provider (dialect + route). The
	// base URL is derived from the strict gateway origin + the provider prefix,
	// so the sandbox reaches only the gateway and the agent speaks the right
	// dialect. Absent => the agent defaults to Anthropic.
	if stage.Provider != "" {
		env["ORCHESTRA_PROVIDER"] = stage.Provider
	}
	if stage.ProviderPrefix != "" {
		env["ORCHESTRA_BASE_URL"] = strings.TrimRight(s.cfg.gatewayStrictBase(), "/") + "/" + strings.Trim(stage.ProviderPrefix, "/")
	}
	if len(stage.Media) > 0 {
		if b, err := json.Marshal(stage.Media); err == nil {
			env["ORCHESTRA_MEDIA"] = string(b)
		}
	}
	if len(stage.Web) > 0 {
		if b, err := json.Marshal(stage.Web); err == nil {
			env["ORCHESTRA_WEB_SEARCH"] = string(b)
		}
	}
	if len(stage.Tools) > 0 {
		if b, err := json.Marshal(stage.Tools); err == nil {
			env["ORCHESTRA_TOOLS"] = string(b)
		}
	}
	// Attribution for the monitoring plane: the agent forwards these to the
	// gateway so every model/tool/RAG call is tied to this run + stage.
	env["ORCHESTRA_RUN"] = run.ID
	env["ORCHESTRA_STAGE"] = stage.ID
	// The stages this one builds on. Only the controller knows the DAG, so it
	// names them; the agent reads their handoff manifests off the worktree and
	// folds them into its prompt. Without this a dependent stage has to be told
	// in prose to go read some agreed filename — a convention that lives only
	// in a prompt, that nothing enforces, and whose absence looks to the agent
	// like a failed read rather than an upstream that produced nothing.
	if len(stage.DependsOn) > 0 {
		env["ORCHESTRA_UPSTREAM"] = strings.Join(stage.DependsOn, ",")
	}
	// Runtime delegation budget: this stage's agent (depth 0) may spawn
	// sub-agents up to maxDepth. The spawn tool is file-based (no host network).
	if run.delegation {
		env["ORCHESTRA_DELEGATE_DEPTH"] = "0"
		env["ORCHESTRA_DELEGATE_MAX"] = strconv.Itoa(run.maxDepth)
	}
	// Pin the image before launching. The policy holds a reference, which may be
	// a moving tag; resolving it host-side to an immutable digest is what makes
	// the run reproducible and its archive line meaningful. It also keeps image
	// pulls a host action — the sandbox never triggers a registry fetch.
	policy := run.policies[stage.ID]
	digest, err := s.docker.Resolve(policy.Ref)
	if err != nil {
		out <- stageResult{id: stage.ID, code: -1, err: fmt.Errorf("image %q (%s): %w", policy.Name, policy.Ref, err)}
		return
	}
	pinned := policy
	pinned.Ref = digest
	run.mu.Lock()
	run.stageByID[stage.ID].Image = policy.Name
	run.stageByID[stage.ID].ImageDigest = digest
	run.mu.Unlock()

	taskID := sanitizeID(run.TaskID + "-" + stage.ID)
	spec := s.buildSpec(taskID, stageWorktree, pinned, stage.Cmd, env, strict, s.sessionFor(run, stage.ID))
	// Scope the container name to this run. Names are what docker enforces
	// uniqueness on, and finished runs keep their containers for log retrieval —
	// without the run id, re-running a task collides with the previous run's
	// leftovers, and two concurrent runs of the same task fight over the name.
	spec.Name = docker.ContainerName(sanitizeID(taskID + "-" + run.ID))
	id, err := s.docker.Create(spec)
	if err != nil {
		out <- stageResult{id: stage.ID, code: -1, err: err}
		return
	}
	run.mu.Lock()
	run.stageByID[stage.ID].ContainerID = id
	run.mu.Unlock()

	// While the stage runs, answer what it asks for through its worktree.
	// Frame sampling is always available — looking at what you produced is not
	// a privilege a template grants — while delegation stays opt-in.
	done := make(chan struct{})
	go s.watchFrames(stageWorktree, strict, done)
	if run.delegation {
		// Sub-agents inherit the parent stage's pinned image, so a delegation
		// cannot become a way to run a different (or newer) image than the run
		// was admitted with.
		go s.watchDelegations(stageWorktree, run, stage, pinned, strict, done)
	}
	code, werr := s.docker.Wait(id)
	close(done)
	// The stage is terminal (done, failed or stopped): archive its output before
	// anything can remove the container, so the log outlives it either way.
	s.archiveStageLog(run.ID, stage.ID, id)

	// Commit the stage's output so dependents (and the final integrate) can build
	// on it. Only on a clean exit; the commit sha is recorded before we report
	// completion, so a dependent launched next reads it safely.
	if mgr != nil && werr == nil && code == 0 {
		sha, cerr := mgr.Commit(stage.ID)
		if cerr != nil {
			out <- stageResult{id: stage.ID, containerID: id, code: -1, err: cerr}
			return
		}
		run.mu.Lock()
		run.stageCommit[stage.ID] = sha
		run.mu.Unlock()
	}

	// Shared mode: record the boundary in the one worktree, so each stage's
	// output is a commit that can be diffed against the stage before it. A
	// stage that changed nothing yields no commit, which is a real outcome
	// rather than an error. Recording must not fail the stage — the work is
	// already done — so a failure is logged and the run continues.
	if rec != nil && werr == nil && code == 0 {
		snap, cerr := rec.Commit(stage.ID)
		if cerr != nil {
			log.Printf("sandbox: recording %s/%s: %v", run.ID, stage.ID, cerr)
		} else if !snap.Empty() {
			run.mu.Lock()
			run.stageCommit[stage.ID] = snap.Commit
			if st := run.stageByID[stage.ID]; st != nil {
				st.Commit, st.Parent, st.Files = snap.Commit, snap.Parent, snap.Files
			}
			run.mu.Unlock()
		}
	}
	out <- stageResult{id: stage.ID, containerID: id, code: code, err: werr}
}

// Dependency resolution outcomes for a pending stage.
const (
	depReady = iota // all dependencies completed successfully
	depWait         // some dependency is still pending/running
	depSkip         // some dependency failed/was skipped/stopped
)

func depState(run *Run, st *StageState) int {
	state := depReady
	for _, id := range st.DependsOn {
		dep := run.stageByID[id]
		switch dep.Status {
		case statusDone:
			// ok
		case statusFailed, statusSkipped, statusStopped:
			return depSkip
		default: // pending/running
			state = depWait
		}
	}
	return state
}

func finalizeStatus(run *Run) string {
	failed := false
	for _, st := range run.Stages {
		if st.Status == statusFailed {
			failed = true
		}
	}
	if run.stopping {
		return statusStopped
	}
	if failed {
		return statusFailed
	}
	return statusDone
}

func (s *Server) getRun(id string) *Run {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	return s.runs[id]
}

func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	run := s.getRun(id)
	if run == nil {
		// The run table is in-memory, so a restart (or a cleared run) leaves only
		// the archive. Serve that: its stages still carry the commits that recorded
		// what each one produced.
		if raw, ok := s.readArchivedRun(id); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write(raw)
			return
		}
		writeErr(w, 404, "unknown run")
		return
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	writeJSON(w, 200, run)
}

func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	run := s.getRun(id)

	// stageID -> containerID. A run that is no longer in memory has no
	// containers, but its stage list and logs are on disk; without reading the
	// stage list from the archive there would be nothing to walk and the logs
	// would sit there unreachable.
	var ids [][2]string
	if run != nil {
		// Snapshot under lock, then fetch logs without holding it (docker calls
		// can block).
		run.mu.Lock()
		for _, st := range run.Stages {
			ids = append(ids, [2]string{st.ID, st.ContainerID})
		}
		run.mu.Unlock()
	} else {
		stageIDs, ok := s.archivedStageIDs(id)
		if !ok {
			writeErr(w, 404, "unknown run")
			return
		}
		for _, sid := range stageIDs {
			ids = append(ids, [2]string{sid, ""})
		}
	}

	logs := map[string]string{}
	for _, pair := range ids {
		// Live container first, archive once it is gone — a stage whose container
		// was removed still has its log.
		if out, ok := s.stageLog(id, pair[0], pair[1]); ok {
			logs[pair[0]] = out
		}
	}
	writeJSON(w, 200, map[string]any{"id": id, "logs": logs})
}

func (s *Server) handleRunStop(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if !decode(w, r, &req) {
		return
	}
	run := s.getRun(req.ID)
	if run == nil {
		writeErr(w, 404, "unknown run")
		return
	}
	run.mu.Lock()
	run.stopping = true
	var running []string
	for _, st := range run.Stages {
		if st.Status == statusRunning && st.ContainerID != "" {
			running = append(running, st.ContainerID)
		}
	}
	run.mu.Unlock()
	for _, id := range running {
		_ = s.docker.Stop(id) // Wait returns, the scheduler marks the stage stopped
	}
	writeJSON(w, 200, map[string]string{"stopping": run.ID})
}

func (s *Server) handleRunRemove(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	run := s.getRun(id)
	if run == nil {
		writeErr(w, 404, "unknown run")
		return
	}
	run.mu.Lock()
	var containers []string
	for _, st := range run.Stages {
		if st.ContainerID != "" {
			containers = append(containers, st.ContainerID)
		}
	}
	run.mu.Unlock()
	for _, cid := range containers {
		_ = s.docker.Remove(cid)
	}
	// The archived stage logs are intentionally kept: this drops the run's live
	// state and its containers, and the archive is what remains to review.
	s.rmu.Lock()
	delete(s.runs, id)
	s.rmu.Unlock()
	writeJSON(w, 200, map[string]string{"removed": id})
}

// sanitizeID reduces an id to docker-name-safe characters.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func runID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "run-0"
	}
	return "run-" + hex.EncodeToString(b[:])
}
