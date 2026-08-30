# Security

moeca runs AI agents on your machine. The whole design follows from one
assumption:

> **An agent is untrusted code.**

Not because the model is malicious, but because what it does is decided at run
time by text it read somewhere — a web search result, a document in your
knowledge base, a file in a repository it was pointed at. Anything that can put
words in front of the model can try to steer it. So the question this design
answers is not "will the model behave" but "what can it reach if it doesn't".

This document says what moeca protects, how, and — the part that matters more —
**what it does not protect**. Every claim below was checked against the code, and
the exceptions are listed as plainly as the guarantees.

![Trust boundaries](docs/images/secure-multi-agents-architecture_v2_en.png)

The red crosses are the point of the picture: they mark paths that **do not
exist**, rather than paths something is watching for. A sandbox cannot reach the
host because there is no route to it, not because a rule says no. The difference
matters when a rule is misconfigured — a missing route stays missing.

## Who this is for

moeca is a **local desktop application for one person**. The security boundary
is your OS user account: anyone who can log in as you can use moeca as you. It
has no login, no roles, and no separation between users, because it is not built
to be shared. If you are looking for multi-user controls, this is the wrong
tool, and no amount of configuration will turn it into the right one.

---

## What is protected

### The agent cannot reach your machine or the internet

Sandboxes run on a Docker network created with `--internal`. Containers on it
have **no route to the host and no route out**. There is exactly one way for an
agent to reach anything: the gateway.

```
orchestra-egress  (--internal — no host route, no internet)
  ├── sandboxes         the agent runs here
  ├── orchestra-gateway the only way out; holds the keys
  └── orchestra-registry package fetches only (see below)

orchestra-upstream (ordinary bridge — the gateway's second NIC)
  └── the gateway and the registry proxy reach the internet from here
```

That second network is what the diagram calls the gateway's secondary NIC. The
gateway is dual-homed **on purpose**: it is the only component on both sides, and
that is what makes it the only door. Nothing else in the sandbox network has a
second address.

The host agent and sandbox controller listen on `127.0.0.1` only, and a sandbox
has no route to the host loopback at all — so those ports are not reachable from
inside a sandbox even by address.

The knowledge indexer is deliberately **not** on this network. An agent searches
your knowledge base through the gateway's `/rag` route; it never talks to the
indexer, and it never sees the files behind it.

### The agent never sees your API keys

Provider secrets live in the **OS keychain**. The gateway's config file on disk
contains `${SECRET}` placeholders, not keys — the real values are pushed into the
gateway over a loopback admin API at startup and held in memory.

The gateway attaches credentials to outbound requests **after** they leave the
sandbox. An agent asks the gateway to call a provider; it cannot read, print, or
exfiltrate the key it is calling with, because the key was never in its address
space. This holds under prompt injection, because there is nothing in the
sandbox to leak.

The admin token that gates that API is handled the same way: the Tauri process
keeps the raw token and hands the gateway only its SHA-256 hash.

### Every run is scoped, and the scope is not self-declared

Sessions are minted per run and per stage, and revoked when it ends. The
knowledge groups a stage may search are **stated by the gateway** on the outbound
request; a group header arriving *from* a sandbox is discarded, never trusted. A
stage cannot widen its own search scope by asking to.

Each session also carries a token/cost budget. It bounds what a runaway loop can
spend before something stops it.

### The container is stripped down

Every sandbox runs with:

- `--read-only` root filesystem (`/tmp` is a tmpfs)
- `--cap-drop ALL` — no raw sockets, no network administration, no privileged
  operations
- `--security-opt no-new-privileges`
- pids, memory and CPU limits

`--cap-drop ALL` is doing more work than it looks. Without `NET_RAW` a container
cannot forge packets, which is what closes the most valuable attack available to
a container that shares a network with others (see *Stages share a network*
below).

### Files: what is writable and what is not

Agents work in a git worktree bind-mounted from the host. **That directory is
writable, and an agent can change anything in it** — that is the point of it.
Everything outside it is not mounted and cannot be reached.

Knowledge folders are mounted **read-only**, and only into the indexer. A test
asserts the `:ro` suffix on every knowledge mount, so this cannot be lost by
accident.

### Dependencies come through a narrow pipe

`npm install` and friends work inside the sandbox, but not by reaching the
internet: a registry proxy on the egress network forwards to **fixed upstreams**
and serves **GET and HEAD only**. An agent can fetch a package. It cannot publish
one, and it cannot use the proxy to reach an arbitrary host.

### Images are pinned and accountable

Sandbox images come from an allowlist. A reference is resolved host-side to an
**immutable digest** before launch, so what runs is what was approved rather than
whatever a tag points at today.

Scheduled (Daily) runs are unattended, so they draw from a **separate list**: an
image is usable there only if it has been explicitly promoted. Promotion is a
deliberate act, not a side effect of using the image once interactively.

Media handling is separated for the same reason. `ffmpeg` and the image and
document parsers live in a `media` image and are deliberately **absent from the
base image**, because putting them everywhere would widen every agent's attack
surface to cover a decade of media-parser bugs.

### There is a tamper-evident record

Every proxied request is written to an append-only audit log where each record's
hash includes the previous record's:

```
hash(n) = SHA-256( hash(n-1) || record(n) )
```

Removing or editing a record breaks the chain from that point on. The log holds
method, path, service, session, status, byte counts, duration and token
estimates, plus a capped excerpt of the request and response.

### Known-dangerous destinations are refused

The gateway denies requests to the cloud metadata endpoint (`169.254.169.254`) —
the classic SSRF target — and can deny specific method/path combinations. The
shipped configuration also blocks repository deletion.

---

## What is **not** protected

This section is the reason the document exists. Read it before you rely on
anything above.

### Stages share a network

All sandboxes in a run join the same egress network and **can reach each other by
IP**.

The "isolated" worktree mode gives each stage its own git worktree. That is a
**filesystem** separation only — the name promises more than the mechanism
delivers, and it is worth saying so plainly.

The diagram above shows a cross between two sandbox boxes. Read it as *no
sandbox exposes a listening port to anything outside its network* — which is
true — and **not** as *stages cannot reach each other*, which is not enforced.

In practice the exposure is narrow: agents make outbound calls and do not listen
on ports, so there is usually nothing to connect to, and `--cap-drop ALL` removes
the raw-socket access that would be needed to impersonate the gateway and steal
another stage's session token. But if you ever run stages you do not equally
trust in the same run — someone else's template, a task from an untrusted source
— that assumption is the one to re-examine first.

### Some agent work runs outside the sandbox

Granting an agent **web search** also gives it provider-side code execution: the
current web-search tool filters its own results by running code on the
provider's machines. That execution is **not** in moeca's sandbox and does not
appear in the sandbox logs.

This is a deliberate acceptance, not an oversight. That code runs on the
provider's infrastructure, with no route to your machine and no network access
of its own, and it can only see the conversation you had already sent. The
boundary it sits behind was crossed by making the request at all.

But it does mean "the agent's code runs in an isolated container" is not the
whole truth, and you should know which half applies.

### Your prompts and knowledge go to third parties

Everything an agent reasons about is sent to whichever model provider you
configured — tasks, knowledge-base excerpts, file contents it read, images it
looked at. moeca does not filter this and does not negotiate retention on your
behalf: what happens to that data is governed by **your agreement with that
provider**, under whatever account you configured.

If your knowledge base contains something that must not leave your machine, no
setting in moeca changes that outcome. Do not index it.

### The model decides what to do

Sandboxing bounds the blast radius; it does not make the agent correct. Within
its worktree an agent can delete your work, commit something wrong, or follow an
instruction embedded in a document it was asked to read. Tools that reach outside
— the ones you configure yourself — do whatever you configured them to do.

Grant tools deliberately. A tool an agent does not have is the only tool it
cannot misuse.

### Trust in what you install

The image allowlist pins digests, but moeca's own dependencies (Go modules, npm
packages) are not currently scanned or attested in CI. You are trusting this
repository and its dependency tree the same way you trust any software you run
locally.

### Not covered

Physical access, a compromised OS account, a malicious Docker daemon, and
supply-chain compromise of Docker Hub itself are all outside what this design can
address.

---

## Reporting a vulnerability

Please report security issues privately rather than in a public issue — open a
[GitHub security advisory](https://github.com/) on this repository so a fix can
land before details circulate.

Useful reports say what boundary was crossed: reaching the host from a sandbox,
reading a provider key, widening a knowledge scope from inside a run, forging or
truncating audit records, or getting egress that does not pass through the
gateway. Those are the claims this design makes, and they are the ones worth
testing.
