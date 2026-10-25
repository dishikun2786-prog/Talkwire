import { startStack } from "./stack.js";

/** Brings the stack up once for all tests; the returned function tears it down. */
export default async function globalSetup(): Promise<() => Promise<void>> {
  const stack = await startStack();
  return () => stack.stop();
}
