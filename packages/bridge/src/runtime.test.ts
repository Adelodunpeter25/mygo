import { describe, expect, test } from "bun:test";
import { CallError, createRuntime } from "./runtime";
import type { Outgoing } from "./types";

type CallMsg = Extract<Outgoing, { t: "call" }>;

function setup() {
  const sent: Outgoing[] = [];
  const rt = createRuntime({ platform: "darwin", windowId: 7, version: "0.0.0-test", secret: "s" }, (m) =>
    sent.push(JSON.parse(m)),
  );
  return { sent, ...rt };
}

describe("call", () => {
  test("resolves with the reply for the current page", async () => {
    const { sent, runtime, internal } = setup();
    const p = runtime.call("Greeter.Greet", "bun", 1);
    const msg = sent[0] as CallMsg;
    expect(msg).toMatchObject({ t: "call", id: 1, m: "Greeter.Greet", a: ["bun", 1] });

    // A reply addressed to another page (stale token) is ignored.
    internal.receive({ t: "reply", id: 1, k: "stale", ok: true, v: "wrong" });
    internal.receive({ t: "reply", id: 1, k: msg.k, ok: true, v: "hello bun" });
    expect(await p).toBe("hello bun");
  });

  test("rejects with a CallError", async () => {
    const { sent, runtime, internal } = setup();
    const p = runtime.call("Files.Read", "/nope");
    const { id, k } = sent[0] as CallMsg;
    internal.receive([{ t: "reply", id, k, ok: false, e: "file not found" }]);
    const err = (await p.catch((e) => e)) as CallError;
    expect(err).toBeInstanceOf(CallError);
    expect(err.message).toBe("file not found");
    expect(err.method).toBe("Files.Read");
  });

  test("concurrent calls resolve independently", async () => {
    const { sent, runtime, internal } = setup();
    const a = runtime.call("A.Do");
    const b = runtime.call("B.Do");
    const [ma, mb] = sent as CallMsg[];
    internal.receive([
      { t: "reply", id: mb!.id, k: mb!.k, ok: true, v: "b" },
      { t: "reply", id: ma!.id, k: ma!.k, ok: true, v: "a" },
    ]);
    expect(await Promise.all([a, b])).toEqual(["a", "b"]);
  });

  test("window controls call built-in methods", () => {
    const { sent, runtime } = setup();
    runtime.window.minimize();
    runtime.window.setTitle("Hi");
    expect(sent.map((m) => (m as CallMsg).m)).toEqual(["mygo:window.Minimize", "mygo:window.SetTitle"]);
    expect((sent[1] as CallMsg).a).toEqual(["Hi"]);
  });
});

describe("events", () => {
  test("on / once / unsubscribe", () => {
    const { runtime, internal } = setup();
    const seen: unknown[] = [];
    const off = runtime.on("tick", (v) => seen.push(["on", v]));
    runtime.once("tick", (v) => seen.push(["once", v]));

    internal.receive({ t: "event", n: "tick", p: 1 });
    internal.receive({ t: "event", n: "tick", p: 2 });
    expect(seen).toEqual([
      ["on", 1],
      ["once", 1],
      ["on", 2],
    ]);

    off();
    internal.receive({ t: "event", n: "tick", p: 3 });
    expect(seen.length).toBe(3);
  });

  test("the same listener can subscribe twice", () => {
    const { runtime, internal } = setup();
    let n = 0;
    const fn = () => n++;
    const off1 = runtime.on("x", fn);
    runtime.on("x", fn);
    internal.receive({ t: "event", n: "x" });
    off1();
    internal.receive({ t: "event", n: "x" });
    expect(n).toBe(3);
  });
});

describe("channels", () => {
  type AckMsg = Extract<Outgoing, { t: "chan-ack" }>;
  type CloseMsg = Extract<Outgoing, { t: "chan-close" }>;

  test("stream values to onmessage, in order, until Go closes them", async () => {
    const { sent, runtime, internal } = setup();
    const got: unknown[] = [];
    let closed = 0;
    const ch = runtime.channel<number>((v) => got.push(v));
    ch.onclose = () => closed++;
    const p = runtime.call("Svc.Stream", "x", ch);
    const { id, k, a } = sent[0] as CallMsg;
    expect(a).toEqual(["x", 1]); // the channel goes as its id

    internal.receive([
      { t: "chan", c: 1, k, p: 1 },
      { t: "chan", c: 1, k: "stale", p: 99 },
      { t: "chan", c: 1, k, p: 2, a: 1 },
    ]);
    expect(got).toEqual([1, 2]);
    expect(sent[1]).toEqual({ t: "chan-ack", c: 1, k, n: 2 } satisfies AckMsg);

    internal.receive([
      { t: "chan", c: 1, k, end: true },
      { t: "reply", id, k, ok: true },
    ]);
    await p;
    expect(ch.closed).toBe(true);
    expect(closed).toBe(1);
    internal.receive({ t: "chan", c: 1, k, p: 3 }); // after the end
    expect(got).toEqual([1, 2]);
  });

  test("iterate values, including those that arrived first", async () => {
    const { sent, runtime, internal } = setup();
    const ch = runtime.channel<string>();
    runtime.call("Svc.Stream", ch);
    const { k } = sent[0] as CallMsg;
    internal.receive([
      { t: "chan", c: 1, k, p: "a" },
      { t: "chan", c: 1, k, p: "b", a: 1 },
    ]);
    expect(sent.length).toBe(1); // not taken yet: no acknowledgment
    const got: string[] = [];
    const loop = (async () => {
      for await (const v of ch) got.push(v);
    })();
    await Promise.resolve();
    expect(sent[1]).toMatchObject({ t: "chan-ack", n: 2 });
    internal.receive([
      { t: "chan", c: 1, k, p: "c" },
      { t: "chan", c: 1, k, end: true },
    ]);
    await loop;
    expect(got).toEqual(["a", "b", "c"]);
  });

  test("leaving the loop closes the channel", async () => {
    const { sent, runtime, internal } = setup();
    const ch = runtime.channel<number>();
    runtime.call("Svc.Stream", ch);
    const { k } = sent[0] as CallMsg;
    internal.receive([
      { t: "chan", c: 1, k, p: 1 },
      { t: "chan", c: 1, k, p: 2 },
    ]);
    for await (const v of ch) {
      if (v === 1) break;
    }
    expect(ch.closed).toBe(true);
    expect(sent[1]).toEqual({ t: "chan-close", c: 1, k } satisfies CloseMsg);
    // A closed channel can not be passed again.
    await expect(runtime.call("Svc.Stream", ch)).rejects.toThrow("one call");
  });

  test("onmessage and a waiting iterator both get their due", async () => {
    const { sent, runtime, internal } = setup();
    const ch = runtime.channel<number>();
    runtime.call("Svc.Stream", ch);
    const { k } = sent[0] as CallMsg;
    const it = ch[Symbol.asyncIterator]();
    const next = it.next();
    internal.receive({ t: "chan", c: 1, k, p: 1 });
    expect(await next).toEqual({ value: 1, done: false });
    const got: number[] = [];
    ch.onmessage = (v) => got.push(v);
    const pending = it.next();
    internal.receive([
      { t: "chan", c: 1, k, p: 2 },
      { t: "chan", c: 1, k, end: true },
    ]);
    expect(got).toEqual([2]);
    expect(await pending).toEqual({ value: undefined, done: true });
  });

  test("a failed call ends its channels", async () => {
    const { sent, runtime, internal } = setup();
    const ch = runtime.channel();
    const p = runtime.call("Svc.Missing", ch);
    const { id, k } = sent[0] as CallMsg;
    const done = (async () => {
      for await (const _ of ch);
    })();
    internal.receive({ t: "reply", id, k, ok: false, e: "method Svc.Missing is not bound" });
    await expect(p).rejects.toThrow("not bound");
    await done;
    expect(ch.closed).toBe(true);
  });
});

describe("page functions", () => {
  type ResultMsg = Extract<Outgoing, { t: "result" }>;

  /** The page's token, from its dom-ready message. */
  function ready(s: ReturnType<typeof setup>) {
    s.ready();
    const msg = s.sent.at(-1) as Extract<Outgoing, { t: "dom-ready" }>;
    expect(msg.t).toBe("dom-ready");
    expect(msg.k).toMatch(/^[0-9a-z]+$/); // Go reads it at the start of results
    return msg.k;
  }

  /** The results posted so far, as they reach Go. */
  const results = async (s: ReturnType<typeof setup>) => {
    await new Promise((r) => setTimeout(r));
    return s.sent.filter((m): m is ResultMsg => m.t === "result");
  };

  test("answer Go with values, promises and errors", async () => {
    const s = setup();
    const k = ready(s);
    const editor = {
      prefix: "> ",
      text() {
        return this.prefix + "hello";
      },
      open: async (name: string, text: string) => {
        await Promise.resolve();
        return name.length + text.length;
      },
      save: () => {},
      fail() {
        throw new Error("disk full");
      },
      reject: () => Promise.reject("nope"),
    };
    s.runtime.expose("Editor", editor);
    s.internal.receive([
      { t: "invoke", id: 1, k, m: "Editor.text", a: [] },
      { t: "invoke", id: 2, k, m: "Editor.open", a: ["a.txt", "abc"] },
      { t: "invoke", id: 3, k, m: "Editor.save" },
      { t: "invoke", id: 4, k, m: "Editor.fail", a: [] },
      { t: "invoke", id: 5, k, m: "Editor.reject", a: [] },
    ]);
    const got = await results(s);
    expect(got.sort((a, b) => a.id - b.id)).toEqual([
      { t: "result", id: 1, k, ok: true, v: "> hello" },
      { t: "result", id: 2, k, ok: true, v: 8 },
      { t: "result", id: 3, k, ok: true },
      { t: "result", id: 4, k, ok: false, e: "disk full" },
      { t: "result", id: 5, k, ok: false, e: "nope" },
    ]);
    // Go reads the id and the token first.
    const raw: string[] = [];
    const t = createRuntime({ platform: "linux", windowId: 1, version: "", secret: "" }, (m) => raw.push(m));
    t.ready();
    const token = (JSON.parse(raw[0]!) as { k: string }).k;
    t.runtime.expose("Editor", editor);
    t.internal.receive({ t: "invoke", id: 12, k: token, m: "Editor.text", a: [] });
    await new Promise((r) => setTimeout(r));
    expect(raw[1]).toStartWith(`{"t":"result","id":12,"k":"${token}",`);
  });

  test("functions the page does not expose", async () => {
    const s = setup();
    const k = ready(s);
    s.runtime.expose("Editor", { text: () => "" });
    s.internal.receive([
      { t: "invoke", id: 1, k, m: "Editor.open", a: [] },
      { t: "invoke", id: 2, k, m: "Other.text", a: [] },
      // What every object inherits is not exposed.
      { t: "invoke", id: 3, k, m: "Editor.toString", a: [] },
      { t: "invoke", id: 4, k, m: "Editor.constructor", a: [] },
      { t: "invoke", id: 5, k, m: "Editor", a: [] },
    ]);
    const got = await results(s);
    expect(got.map((r) => [r.id, r.ok, r.missing])).toEqual([
      [1, false, true],
      [2, false, true],
      [3, false, true],
      [4, false, true],
      [5, false, true],
    ]);
  });

  test("invocations meant for another page are ignored", async () => {
    const s = setup();
    ready(s);
    let calls = 0;
    s.runtime.expose("Editor", { text: () => calls++ });
    s.internal.receive({ t: "invoke", id: 1, k: "stale", m: "Editor.text", a: [] });
    expect(calls).toBe(0);
    expect(await results(s)).toEqual([]);
  });

  test("the latest exposure answers, until it is withdrawn", async () => {
    const s = setup();
    const k = ready(s);
    const first = s.runtime.expose("Editor", { text: () => "first", open: () => "open" });
    const second = s.runtime.expose("Editor", { text: () => "second" });
    const text = async (id: number) => {
      s.internal.receive({ t: "invoke", id, k, m: "Editor.text", a: [] });
      return (await results(s)).find((r) => r.id === id);
    };
    expect((await text(1))?.v).toBe("second");
    // Functions the latest does not have come from earlier ones.
    s.internal.receive({ t: "invoke", id: 2, k, m: "Editor.open", a: [] });
    expect((await results(s)).find((r) => r.id === 2)?.v).toBe("open");
    second();
    second(); // withdrawing twice is harmless
    expect((await text(3))?.v).toBe("first");
    first();
    expect((await text(4))?.missing).toBe(true);
    // The same object can be exposed twice and withdrawn once.
    const fns = { text: () => "same" };
    const a = s.runtime.expose("Editor", fns);
    s.runtime.expose("Editor", fns);
    a();
    expect((await text(5))?.v).toBe("same");
    expect(() => s.runtime.expose("Editor", null as unknown as object)).toThrow(TypeError);
  });

  test("values JSON cannot encode fail the call", async () => {
    const s = setup();
    const k = ready(s);
    const cycle: Record<string, unknown> = {};
    cycle.self = cycle;
    s.runtime.expose("Data", { big: () => 1n, cycle: () => cycle });
    s.internal.receive([
      { t: "invoke", id: 1, k, m: "Data.big", a: [] },
      { t: "invoke", id: 2, k, m: "Data.cycle", a: [] },
    ]);
    const got = await results(s);
    expect(got.map((r) => [r.id, r.ok])).toEqual([
      [1, false],
      [2, false],
    ]);
    expect(got[0]!.e).toStartWith("cannot encode the result:");
  });

  test("functions run in the order Go sent them, among events", () => {
    const s = setup();
    const k = ready(s);
    const order: string[] = [];
    s.runtime.on("changed", () => order.push("event"));
    s.runtime.expose("Editor", { open: (name: string) => void order.push(name) });
    s.internal.receive([
      { t: "invoke", id: 1, k, m: "Editor.open", a: ["a"] },
      { t: "event", n: "changed" },
      { t: "invoke", id: 2, k, m: "Editor.open", a: ["b"] },
    ]);
    expect(order).toEqual(["a", "event", "b"]);
  });
});

test("runtime object is frozen", () => {
  const { runtime } = setup();
  expect(runtime.platform).toBe("darwin");
  expect(runtime.windowId).toBe(7);
  expect(Object.isFrozen(runtime)).toBe(true);
  expect(Object.isFrozen(runtime.window)).toBe(true);
});
