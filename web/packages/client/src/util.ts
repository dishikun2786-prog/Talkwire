/** Small observable primitives the client and the React bindings share. */

export type Listener<T> = (value: T) => void;
export type Unsubscribe = () => void;

/** A value that changes over time; `subscribe` fires on every change, not on subscribe. */
export interface ReadonlySignal<T> {
  readonly value: T;
  subscribe(listener: Listener<T>): Unsubscribe;
}

export class Signal<T> implements ReadonlySignal<T> {
  private readonly listeners = new Set<Listener<T>>();

  constructor(
    private current: T,
    private readonly equals: (a: T, b: T) => boolean = Object.is,
  ) {}

  get value(): T {
    return this.current;
  }

  set(next: T): void {
    if (this.equals(this.current, next)) return;
    this.current = next;
    for (const l of [...this.listeners]) {
      try {
        l(next);
      } catch (e) {
        reportListenerError(e);
      }
    }
  }

  update(fn: (current: T) => T): void {
    this.set(fn(this.current));
  }

  subscribe(listener: Listener<T>): Unsubscribe {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

/** A stream of events; late subscribers miss earlier events. */
export class Emitter<T> {
  private readonly listeners = new Set<Listener<T>>();

  on(listener: Listener<T>): Unsubscribe {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  emit(value: T): void {
    for (const l of [...this.listeners]) {
      try {
        l(value);
      } catch (e) {
        reportListenerError(e);
      }
    }
  }
}

function reportListenerError(e: unknown): void {
  // A listener's bug must not break the client's own bookkeeping.
  setTimeout(() => {
    throw e;
  });
}

export interface Deferred<T> {
  readonly promise: Promise<T>;
  readonly settled: boolean;
  resolve(value: T): void;
  reject(error: unknown): void;
}

export function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  let settled = false;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  // A deferred nobody awaits must not surface as an unhandled rejection.
  promise.catch(() => {});
  return {
    promise,
    get settled() {
      return settled;
    },
    resolve(v) {
      if (settled) return;
      settled = true;
      resolve(v);
    },
    reject(e) {
      if (settled) return;
      settled = true;
      reject(e);
    },
  };
}

export const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/** Resolves with the promise's value, or `undefined` once `ms` passes. */
export function withTimeout<T>(promise: Promise<T>, ms: number): Promise<T | undefined> {
  return new Promise<T | undefined>((resolve, reject) => {
    const t = setTimeout(() => resolve(undefined), ms);
    promise.then(
      (v) => {
        clearTimeout(t);
        resolve(v);
      },
      (e) => {
        clearTimeout(t);
        reject(e);
      },
    );
  });
}

/** The first value of `signal` that passes `test`, or `undefined` after `ms`. */
export function firstValue<T>(signal: ReadonlySignal<T>, test: (v: T) => boolean, ms: number): Promise<T | undefined> {
  if (test(signal.value)) return Promise.resolve(signal.value);
  return new Promise((resolve) => {
    const t = setTimeout(() => {
      off();
      resolve(undefined);
    }, ms);
    const off = signal.subscribe((v) => {
      if (!test(v)) return;
      clearTimeout(t);
      off();
      resolve(v);
    });
  });
}

/** A random id of base-36 characters, for client message ids and request ids. */
export function randomId(length = 16): string {
  const bytes = new Uint8Array(length);
  globalThis.crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) out += (b % 36).toString(36);
  return out;
}
