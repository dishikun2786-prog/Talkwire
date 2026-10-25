import { Signal } from "@imkit/client";
import { ToneRinger, ringToneFor, ringWith, type CallRinger, type CallState, type ToneContext } from "@imkit/call";
import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

class LogRinger implements CallRinger {
  readonly log: string[] = [];
  ring() {
    this.log.push("ring");
  }
  ringback() {
    this.log.push("ringback");
  }
  stop() {
    this.log.push("stop");
  }
}

const incoming = (id: string): CallState => ({ phase: "incoming", callSessionId: id, callType: "voice", ringTimeoutSeconds: 45, since: 1 });
const active = (id: string): CallState => ({ phase: "active", callSessionId: id, callType: "voice", connecting: true, muted: false, since: 2 });

describe("ringWith", () => {
  it("rings while a call rings, ringbacks while calling, and is quiet otherwise", () => {
    const state = new Signal<CallState>({ phase: "idle" });
    const ringer = new LogRinger();
    const off = ringWith({ state }, ringer);
    expect(ringer.log).toEqual([]);

    state.set(incoming("a"));
    state.set({ ...(incoming("a") as Extract<CallState, { phase: "incoming" }>), callerName: "Bob" });
    expect(ringer.log, "the same call does not start over").toEqual(["ring"]);
    state.set(incoming("b"));
    expect(ringer.log, "another call rings anew").toEqual(["ring", "stop", "ring"]);
    state.set(active("b"));
    state.set({ ...(active("b") as Extract<CallState, { phase: "active" }>), connecting: false });
    expect(ringer.log).toEqual(["ring", "stop", "ring", "stop"]);

    state.set({ phase: "outgoing", peer: "uid_bob", callType: "video", since: 3 });
    state.set({ phase: "outgoing", peer: "uid_bob", callType: "video", callSessionId: "c", since: 3 });
    expect(ringer.log.slice(4), "the server's ack does not restart it").toEqual(["ringback"]);
    state.set({ phase: "ended", callSessionId: "c", reason: "no_answer", since: 4 });
    expect(ringer.log.slice(4)).toEqual(["ringback", "stop"]);

    state.set(incoming("d"));
    off();
    expect(ringer.log.slice(6), "unsubscribing stops the sound").toEqual(["ring", "stop"]);
    state.set(incoming("e"));
    expect(ringer.log.length).toBe(8);
  });

  it("starts with the state it finds", () => {
    const ringer = new LogRinger();
    const off = ringWith({ state: new Signal<CallState>(incoming("x")) }, ringer);
    expect(ringer.log).toEqual(["ring"]);
    off();
    expect(ringToneFor({ phase: "idle" })).toBeNull();
  });
});

/** Web Audio as far as ToneRinger uses it, keeping the notes it was asked for. */
class FakeContext implements ToneContext {
  currentTime = 10;
  state = "suspended";
  destination = {};
  resumed = 0;
  notes: { freq: number; start: number; stop: number; stoppedEarly?: number }[] = [];
  async resume() {
    this.resumed++;
    this.state = "running";
  }
  createOscillator() {
    const note = { freq: 0, start: 0, stop: 0 } as FakeContext["notes"][number];
    this.notes.push(note);
    return {
      frequency: {
        set value(v: number) {
          note.freq = v;
        },
        get value() {
          return note.freq;
        },
      },
      connect: () => undefined,
      start: (at: number) => {
        note.start = at;
      },
      stop: (at: number) => {
        if (note.stop) note.stoppedEarly = at;
        else note.stop = at;
      },
    };
  }
  createGain() {
    return { gain: { setValueAtTime: () => undefined, linearRampToValueAtTime: () => undefined }, connect: () => undefined };
  }
}

describe("ToneRinger", () => {
  it("chimes every three seconds, vibrating, until stopped", () => {
    vi.useFakeTimers();
    const vibrate = vi.fn(() => true);
    vi.stubGlobal("navigator", { vibrate });
    const ctx = new FakeContext();
    let made = 0;
    const ringer = new ToneRinger({ audioContext: () => (made++, ctx) });
    ringer.ring();
    expect(ctx.resumed, "a context the page has not woken is resumed").toBe(1);
    expect(ctx.notes.map((n) => n.freq)).toEqual([659.25, 783.99, 659.25, 783.99]);
    expect(ctx.notes[0]!.start).toBeCloseTo(10.05);
    expect(vibrate).toHaveBeenLastCalledWith([400, 200, 400]);
    vi.advanceTimersByTime(3_000);
    expect(ctx.notes.length).toBe(8);

    ringer.stop();
    expect(ctx.notes.slice(4).every((n) => n.stoppedEarly === 10)).toBe(true);
    expect(vibrate).toHaveBeenLastCalledWith(0);
    vi.advanceTimersByTime(10_000);
    expect(ctx.notes.length, "nothing after stop").toBe(8);

    ringer.ringback();
    expect(ctx.notes.slice(8).map((n) => [n.freq, n.stop - n.start])).toEqual([[425, 1]]);
    vi.advanceTimersByTime(5_000);
    expect(ctx.notes.length).toBe(10);
    ringer.stop();
    expect(made, "one context for all sounds").toBe(1);
  });

  it("is silent where there is no Web Audio", () => {
    vi.stubGlobal("AudioContext", undefined);
    const ringer = new ToneRinger({ vibrate: false });
    expect(() => {
      ringer.ring();
      ringer.ringback();
      ringer.stop();
    }).not.toThrow();
    const failing = new ToneRinger({
      audioContext: () => {
        throw new Error("blocked");
      },
    });
    expect(() => failing.ring()).not.toThrow();
  });
});
