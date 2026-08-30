// The knowledge references the RAG indexer reads.
import i18n from "@/i18n";
//
// These go through Tauri commands rather than an HTTP service because a local
// folder has to be bind-mounted into the indexer container, and mounting is a
// host action. A bind mount cannot be added to a container that is already
// running, so adding or removing a reference restarts the indexer — the call
// returns once the new container is up, and the index rebuilds from there.
//
// In a plain browser (Vite dev without the desktop shell) there is no shell to
// mount anything, so these are unavailable and the panel says so.

import { open } from "@tauri-apps/plugin-dialog";

/** Image captioning: which vision model describes pictures at ingest, if any.
 *  An empty model is off. */
export interface CaptionSetting {
  model: string;
  prefix: string;
}

/** One registered reference: a local folder, or an HTTPS document. */
export interface KnowledgeSource {
  kind: "local" | "external";
  /** local: the host folder path. external: the https URL. */
  path: string;
}

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;

function invoker(): Invoke | null {
  const g = (window as unknown as { __TAURI__?: { core?: { invoke?: Invoke } } }).__TAURI__;
  return g?.core?.invoke ?? null;
}

/** True inside the Tauri desktop shell (reference management available). */
export function isDesktop(): boolean {
  return invoker() != null;
}

export const knowledgeSources = {
  async list(): Promise<KnowledgeSource[]> {
    const f = invoker();
    if (!f) return [];
    return f<KnowledgeSource[]>("knowledge_sources");
  },

  /** Registers a reference and restarts the indexer. Rejects with the shell's
   *  message when the path is not a folder, or the URL is not https. */
  async add(kind: KnowledgeSource["kind"], path: string): Promise<KnowledgeSource[]> {
    const f = invoker();
    if (!f) throw new Error(i18n.t("knowledge.desktopOnly"));
    return f<KnowledgeSource[]>("knowledge_source_add", { kind, path });
  },

  async remove(path: string): Promise<KnowledgeSource[]> {
    const f = invoker();
    if (!f) throw new Error(i18n.t("knowledge.desktopOnly"));
    return f<KnowledgeSource[]>("knowledge_source_remove", { path });
  },

  /** Whether images are described by a vision model at ingest, and by which.
   *
   *  An empty model means off, which is the default and has to be: a caption
   *  costs a model call per picture, so registering a folder of screenshots
   *  must not quietly start spending. Reading it outside the desktop shell
   *  answers "off" rather than throwing — there is nothing to configure there.
   */
  async caption(): Promise<CaptionSetting> {
    const f = invoker();
    if (!f) return { model: "", prefix: "" };
    return f<CaptionSetting>("knowledge_caption");
  },

  /** Sets it and restarts the indexer, so the next build reads it. Turning it
   *  on means that build describes every picture it has not seen before. */
  async setCaption(model: string, prefix = ""): Promise<CaptionSetting> {
    const f = invoker();
    if (!f) throw new Error(i18n.t("knowledge.desktopOnly"));
    return f<CaptionSetting>("knowledge_caption_set", { model, prefix });
  },

  /** Native folder picker. Returns null when the user cancels. */
  async pickFolder(): Promise<string | null> {
    if (!isDesktop()) return null;
    const picked = await open({ directory: true, multiple: false, title: i18n.t("knowledge.pickFolder") });
    return typeof picked === "string" ? picked : null;
  },
};
