// The public types are those of the mygo-runtime package.
import type { Platform } from "../../runtime/src/types";

export type { Channel, Platform, Runtime, WindowControls } from "../../runtime/src/types";

/** Configuration injected by the Go side in front of the bridge. */
export interface BridgeConfig {
  platform: Platform;
  windowId: number;
  version: string;
  /** Prefixes every message, so Go can tell ours from other frames'. */
  secret: string;
}

/** Messages posted from the page to Go. */
export type Outgoing =
  | { t: "call"; id: number; k: string; m: string; a: unknown[] }
  /** Channel c took the values up to the n-th. */
  | { t: "chan-ack"; c: number; k: string; n: number }
  | { t: "chan-close"; c: number; k: string }
  /**
   * The answer to the invocation id of a page function: its value, what it
   * threw, or that the page does not expose it. Go reads id and k first.
   */
  | { t: "result"; id: number; k: string; ok: boolean; v?: unknown; e?: string; missing?: true }
  /** The DOM is ready; k is the page's token, which Go's invocations name. */
  | { t: "dom-ready"; k: string }
  | { t: "drag" }
  | { t: "dblclick" };

/** Messages delivered from Go to the page. */
export type Incoming =
  | { t: "event"; n: string; p?: unknown }
  | { t: "reply"; id: number; k: string; ok: boolean; v?: unknown; e?: string }
  /** A value p of channel c, which asks for an acknowledgment with a, or its end. */
  | { t: "chan"; c: number; k: string; p?: unknown; a?: 1; end?: true }
  /** Go calls the page function m, e.g. "Editor.text", with the arguments a. */
  | { t: "invoke"; id: number; k: string; m: string; a?: unknown[] };
