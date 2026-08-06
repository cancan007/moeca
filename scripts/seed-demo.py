#!/usr/bin/env python3
"""Seed the demo knowledge base so the app has something to show.

    ./scripts/seed-demo.py            # register the folder, then seed the graph
    ./scripts/seed-demo.py --status   # just report what is there now

Runs in two halves, because they need different things to be true:

  1. Registering examples/knowledge with the indexer only touches a file, so it
     works whether or not the app is running. It takes effect on the next
     launch: a bind mount cannot be added to a running container.
  2. Seeding the graph (organizations, projects, groups, relations) and
     assigning sources to groups goes through the running services, so it needs
     the app up and the index built.

Both halves are idempotent — running this twice changes nothing the second
time. Nothing here is a fixture the app knows about: it is the same HTTP the UI
uses, so anything seeded can be edited or deleted on screen afterwards.

Standard library only; no venv, no install.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CORPUS = REPO / "examples" / "knowledge"
HOSTAGENT = os.environ.get("ORCHESTRA_HOSTAGENT_URL", "http://127.0.0.1:8788")
RAG = os.environ.get("ORCHESTRA_RAG_URL", "http://127.0.0.1:8790")
SANDBOX = os.environ.get("ORCHESTRA_SANDBOX_URL", "http://127.0.0.1:8789")
STATE = Path.home() / "Library" / "Application Support" / "orchestra"

ORG = "Acme Inc."
PROJECTS = ["決済基盤", "在庫管理"]

# Groups, and which indexed sources each one covers. A prefix match keeps this
# readable and survives files being added to the corpus later.
GROUPS: list[dict] = [
    {
        "name": "決済仕様",
        "color": "#4f9dff",
        "owner": "payments-team",
        "description": "決済基盤の設計方針と API 契約",
        "projects": ["決済基盤"],
        "prefixes": ["docs/payments-overview.md", "docs/api-reference.md", "specs/"],
    },
    {
        "name": "運用ルール",
        "color": "#3fbf8f",
        "owner": "sre",
        "description": "リトライ・SLO など、実行時の判断に効くもの",
        "projects": ["決済基盤", "在庫管理"],
        "prefixes": ["docs/retry-policy.md", "data/slo-targets.tsv"],
    },
    {
        "name": "過去障害",
        "color": "#e0a83e",
        "owner": "sre",
        "description": "障害報告と、そこから来た制約",
        "projects": ["決済基盤"],
        "prefixes": ["docs/incident-", "data/incidents.csv"],
    },
    {
        "name": "在庫仕様",
        "color": "#34d3e0",
        "owner": "inventory-team",
        "description": "在庫の状態遷移と引当の寿命",
        "projects": ["在庫管理"],
        "prefixes": ["docs/inventory-overview.md", "design/inventory-states.svg"],
    },
    {
        "name": "デモ資料",
        "color": "#b08ad9",
        "owner": "docs",
        "description": "動画・図版など、中身が索引されない参照先",
        "projects": ["決済基盤"],
        "prefixes": ["media/", "design/payment-flow.svg", "design/checkout-screen.png", "README.md"],
    },
]

# Arrows are for a human to read; retrieval walks them in both directions.
RELATIONS = [
    ("過去障害", "決済仕様", "derives-from"),
    ("運用ルール", "決済仕様", "requires"),
    ("在庫仕様", "決済仕様", "references"),
    ("デモ資料", "決済仕様", "references"),
]


# --- plumbing ---------------------------------------------------------------

def call(base: str, path: str, method: str = "GET", body: dict | None = None, timeout: float = 10):
    req = urllib.request.Request(
        base + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as res:
        raw = res.read()
    return json.loads(raw) if raw else {}


def up(base: str) -> bool:
    try:
        urllib.request.urlopen(base + "/health", timeout=2).read()
        return True
    except Exception:
        return False


def say(msg: str) -> None:
    print(f"  {msg}")


# --- half 1: register the corpus with the indexer ---------------------------

def register_corpus() -> bool:
    """Adds examples/knowledge to the indexer's reference list. Returns True if
    the list changed, which is what decides whether a restart is needed."""
    if not CORPUS.is_dir():
        sys.exit(f"corpus not found: {CORPUS}")
    STATE.mkdir(parents=True, exist_ok=True)
    path = STATE / "knowledge-sources.json"

    refs = []
    if path.exists():
        try:
            refs = json.loads(path.read_text())
        except json.JSONDecodeError:
            # The app treats a corrupt list as no list; do the same rather than
            # overwriting something the user may still want.
            sys.exit(f"{path} is not valid JSON — fix or delete it first")

    target = str(CORPUS)
    if any(r.get("kind") == "local" and r.get("path") == target for r in refs):
        say(f"参照先は登録済み: {target}")
        return False
    refs.append({"kind": "local", "path": target})
    path.write_text(json.dumps(refs, ensure_ascii=False, indent=2))
    say(f"参照先を登録: {target}")
    return True


# --- half 2: the authored graph --------------------------------------------

def seed_graph() -> dict:
    graph = call(HOSTAGENT, "/knowledge")

    orgs = {o["name"]: o["id"] for o in graph.get("orgs") or []}
    if ORG in orgs:
        org_id = orgs[ORG]
        say(f"organization は既存: {ORG}")
    else:
        org_id = call(HOSTAGENT, "/knowledge/org", "POST", {"name": ORG})["id"]
        say(f"organization を作成: {ORG}")

    projects = {p["name"]: p["id"] for p in graph.get("projects") or [] if p["orgId"] == org_id}
    for name in PROJECTS:
        if name in projects:
            continue
        projects[name] = call(HOSTAGENT, "/knowledge/project", "POST",
                              {"name": name, "orgId": org_id})["id"]
        say(f"project を作成: {name}")

    groups = {g["name"]: g["id"] for g in graph.get("groups") or []}
    for spec in GROUPS:
        if spec["name"] in groups:
            continue
        groups[spec["name"]] = call(HOSTAGENT, "/knowledge/group", "POST", {
            "name": spec["name"], "color": spec["color"],
            "owner": spec["owner"], "description": spec["description"],
        })["id"]
        say(f"group を作成: {spec['name']}")

    existing = {(r["from"], r["to"], r["type"]) for r in graph.get("relations") or []}
    for a, b, kind in RELATIONS:
        if a not in groups or b not in groups:
            continue
        key = (groups[a], groups[b], kind)
        if key in existing or (groups[b], groups[a], kind) in existing:
            continue
        call(HOSTAGENT, "/knowledge/relation", "POST",
             {"from": groups[a], "to": groups[b], "type": kind})
        say(f"relation を作成: {a} → {b} ({kind})")

    return {"org": org_id, "projects": projects, "groups": groups}


def assign_sources(ids: dict, sources: list[str]) -> None:
    """Puts each indexed source into the groups whose prefixes match, and links
    every group to its projects. Both lists are submitted whole because the
    server replaces them — a partial list would revoke what was omitted."""
    for spec in GROUPS:
        gid = ids["groups"].get(spec["name"])
        if not gid:
            continue
        mine = [s for s in sources if any(s.startswith(p) for p in spec["prefixes"])]
        call(HOSTAGENT, "/knowledge/group/links", "POST", {
            "groupId": gid,
            "projects": [ids["projects"][p] for p in spec["projects"] if p in ids["projects"]],
            "sources": mine,
        })
        say(f"{spec['name']}: {len(mine)} 件のソースを割り当て")


def wait_for_index(trigger: bool = True) -> list[str]:
    """Triggers a rebuild and waits for it. Returns the indexed source paths."""
    if trigger:
        try:
            call(RAG, "/index", "POST")
            say("再インデックスを開始…")
        except urllib.error.HTTPError as e:
            say(f"再インデックスを開始できません: {e}")

    deadline = time.time() + 300
    while time.time() < deadline:
        st = call(RAG, "/status")
        if not st.get("building"):
            srcs = [s["path"] for s in st.get("sources") or []]
            if st.get("lastError"):
                say(f"索引エラー: {st['lastError']}")
            return srcs
        time.sleep(2)
    say("索引の完了を待てませんでした（タイムアウト）")
    return []


# --- the trace ---

# One agent stage that has to look things up before it can answer.
#
# The Knowledge trace is built from what the gateway recorded, so it does not
# exist until some run has actually retrieved something. Nothing else in this
# script can manufacture it: a trace is evidence, and evidence has to be earned
# by a real retrieval. So this fires a real run — a real model call, a real
# rag_search, a real artifact — and the trace appears because it happened.
#
# Three questions rather than one, because a single query traces to a single
# spot on the graph. Three spread the trace across groups, which is what makes
# the screen worth looking at: some nodes lit, most not.
TRACE_TASK = (
    "決済基盤について、(1) 冪等キーの保持期間 (2) 2026年3月の二重計上の原因 "
    "(3) リトライの上限回数 の3点を、rag_search でそれぞれ検索して確認し、"
    "根拠のファイル名を添えて findings.md にまとめてください。"
)

RAG_SEARCH_TOOL = {
    "name": "rag_search",
    "description": "登録済みナレッジベースを検索し、関連する文書チャンクを取得する。",
    "inputSchema": {
        "type": "object",
        "properties": {"query": {"type": "string", "description": "検索クエリ"}},
        "required": ["query"],
    },
    "method": "POST",
    "path": "/rag/search",
    "headers": {},
    "body": '{"query":"{{query}}","k":5}',
    "targetHeader": "",
}


def fire_trace_run(model: str, provider: str, prefix: str) -> str | None:
    """Runs one RAG-using agent and returns the run id."""
    workdir = STATE / "demo-trace"
    workdir.mkdir(parents=True, exist_ok=True)
    req = {
        "taskId": "demo-trace",
        "worktreePath": str(workdir),
        "isolation": "strict",
        "maxParallel": 1,
        "stages": [{
            "id": "researcher",
            "name": "researcher",
            "role": "調査",
            "model": model,
            "provider": provider,
            "providerPrefix": prefix,
            "maxTokens": 2000,
            "system": "あなたは社内ナレッジの調査担当です。必ず rag_search で根拠を引いてから答えてください。",
            "task": TRACE_TASK,
            "dependsOn": [],
            "tools": [RAG_SEARCH_TOOL],
        }],
    }
    run_id = call(SANDBOX, "/run", "POST", req, timeout=30).get("runId")
    if not run_id:
        say("ランを開始できませんでした")
        return None
    say(f"ラン {run_id} を開始（モデル呼び出しが走ります）")

    deadline = time.time() + 600
    while time.time() < deadline:
        st = call(SANDBOX, f"/run?id={run_id}")
        status = st.get("status")
        if status in ("done", "failed", "stopped"):
            for stage in st.get("stages") or []:
                detail = f" — {stage['error'][:120]}" if stage.get("error") else ""
                say(f"  stage {stage['id']}: {stage['status']}{detail}")
            return run_id if status == "done" else None
        time.sleep(5)
    say("ランの完了を待てませんでした（タイムアウト）")
    return None


# --- reporting --------------------------------------------------------------

def report() -> None:
    print("状態:")
    for name, base in (("hostagent", HOSTAGENT), ("ragindex", RAG)):
        print(f"  {name:<10} {base:<24} {'起動中' if up(base) else '未起動'}")
    if up(RAG):
        st = call(RAG, "/status")
        mode = st.get("embedMode", "gateway")
        print(f"  索引       {st.get('chunks', 0)} chunks / {len(st.get('sources') or [])} files"
              f" · embedMode={mode}")
        if mode == "offline":
            print("             ※ ベクトルはモデルではなくローカル近似です（デモ用）")
    if up(HOSTAGENT):
        g = call(HOSTAGENT, "/knowledge")
        print(f"  グラフ     {len(g.get('orgs') or [])} org / {len(g.get('projects') or [])} project"
              f" / {len(g.get('groups') or [])} group / {len(g.get('relations') or [])} relation")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--status", action="store_true", help="現状を表示するだけ")
    ap.add_argument("--no-reindex", action="store_true", help="再インデックスを走らせない")
    ap.add_argument("--trace", action="store_true",
                    help="RAG を使うエージェントを1回実行し、Knowledge のトレースを作る（モデル課金が発生します）")
    ap.add_argument("--model", default="claude-haiku-4-5-20251001", help="--trace で使うモデル")
    ap.add_argument("--provider", default="anthropic", help="--trace で使うプロバイダの dialect")
    ap.add_argument("--prefix", default="/anthropic/", help="--trace で使うゲートウェイのルート")
    args = ap.parse_args()

    if args.status:
        report()
        return

    if args.trace:
        if not up(SANDBOX):
            sys.exit("sandbox コントローラ (127.0.0.1:8789) が起動していません")
        print("▸ トレース用のラン")
        run_id = fire_trace_run(args.model, args.provider, args.prefix)
        if not run_id:
            sys.exit(1)
        print()
        print(f"Audit タブのこのランから「ナレッジのトレース」を押すか、")
        print(f"Knowledge タブを ?run={run_id} 付きで開いてください。")
        return

    print("▸ 参照先の登録")
    changed = register_corpus()

    if not up(HOSTAGENT):
        print()
        print("hostagent が起動していません。ここまでで登録は済んでいるので、")
        print("  ./scripts/dev.sh          # 埋め込みプロバイダを設定済みの場合")
        print("  ORCHESTRA_EMBED_MODE=offline ./scripts/dev.sh   # キー無しで動かす場合")
        print("でアプリを起動し、もう一度このスクリプトを実行してください。")
        return

    if changed:
        print()
        print("参照先を新しく登録しました。バインドマウントは起動中のコンテナに追加できないため、")
        print("アプリを再起動してから、もう一度このスクリプトを実行してください。")
        return

    print("▸ ナレッジグラフ")
    ids = seed_graph()

    if not up(RAG):
        say("ragindex が未起動のため、ソースの割り当ては省略しました")
        return

    print("▸ 索引")
    sources = wait_for_index(trigger=not args.no_reindex)
    if not sources:
        say("索引にソースがありません。フォルダがマウントされているか、埋め込みが通っているか確認してください")
        return
    say(f"{len(sources)} 件のソースを索引")

    print("▸ ソースの割り当て")
    assign_sources(ids, sources)

    print()
    report()
    print()
    print("Knowledge タブを開くと、ノード・group・relation が出ているはずです。")


if __name__ == "__main__":
    try:
        main()
    except urllib.error.HTTPError as e:
        sys.exit(f"HTTP {e.code} {e.reason}: {e.read().decode(errors='replace')[:300]}")
    except urllib.error.URLError as e:
        sys.exit(f"接続できません: {e.reason}")
