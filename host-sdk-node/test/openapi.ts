// Validators for the requests and responses of an OpenAPI 3.1 contract in
// contract/host-api/, for contract tests in both directions.

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import { parse } from "yaml";

export const contractDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "contract", "host-api");

type Json = Record<string, unknown>;

const escape = (s: string) => s.replace(/~/g, "~0").replace(/\//g, "~1");

export class Contract {
  readonly doc: Json;
  private readonly ajv: Ajv2020;
  private readonly coercing: Ajv2020;
  private readonly id: string;
  private readonly cache = new Map<string, ValidateFunction>();

  constructor(file: string) {
    this.doc = parse(readFileSync(path.join(contractDir, file), "utf8")) as Json;
    this.id = `https://contract.talkwire.invalid/${file}.json`;
    // The whole document is registered so "#/components/..." refs resolve;
    // non-schema keywords (openapi, paths, x-talkwire) are ignored.
    this.ajv = new Ajv2020({ strict: false, allErrors: true });
    this.ajv.addSchema(this.doc, this.id);
    // Query parameters arrive as strings.
    this.coercing = new Ajv2020({ strict: false, allErrors: true, coerceTypes: true });
    this.coercing.addSchema(this.doc, this.id);
  }

  /** Follows a non-schema $ref (a response or a parameter) inside the document. */
  private deref<T>(node: T): T {
    const ref = (node as Json | undefined)?.$ref;
    if (typeof ref !== "string") return node;
    let cur: unknown = this.doc;
    for (const part of ref.replace(/^#\//, "").split("/")) cur = (cur as Json)[part.replace(/~1/g, "/").replace(/~0/g, "~")];
    return this.deref(cur as T);
  }

  operation(method: string, route: string): Json {
    const op = ((this.doc.paths as Json)[route] as Json | undefined)?.[method.toLowerCase()] as Json | undefined;
    if (!op) throw new Error(`${method} ${route} is not in the contract`);
    return op;
  }

  /** Every operation: [method, path]. */
  operations(): Array<[string, string]> {
    const out: Array<[string, string]> = [];
    for (const [route, item] of Object.entries(this.doc.paths as Json)) {
      for (const method of Object.keys(item as Json)) {
        if (["get", "post", "put", "patch", "delete"].includes(method)) out.push([method.toUpperCase(), route]);
      }
    }
    return out;
  }

  private cached(key: string, build: () => ValidateFunction): ValidateFunction {
    let v = this.cache.get(key);
    if (!v) this.cache.set(key, (v = build()));
    return v;
  }

  private ref(pointer: string): ValidateFunction {
    return this.cached(pointer, () => this.ajv.compile({ $ref: `${this.id}#${pointer}` }));
  }

  requestBody(method: string, route: string): ValidateFunction | null {
    const op = this.operation(method, route);
    if (!(op.requestBody as Json | undefined)?.content) return null;
    return this.ref(`/paths/${escape(route)}/${method.toLowerCase()}/requestBody/content/application~1json/schema`);
  }

  /** Validates a query string object: declared parameters only, required ones present. */
  query(method: string, route: string): ValidateFunction {
    const op = this.operation(method, route);
    const params = ((op.parameters as unknown[] | undefined) ?? []).map((p) => this.deref(p as Json));
    const properties: Json = {};
    const required: string[] = [];
    for (const p of params) {
      if (p.in !== "query") continue;
      properties[p.name as string] = p.schema;
      if (p.required) required.push(p.name as string);
    }
    // Inline the parameter schemas, keeping their refs resolvable.
    return this.cached(`query ${method} ${route}`, () => this.coercing.compile({
      $id: `https://contract.talkwire.invalid/query/${method}${route}`,
      type: "object",
      properties: JSON.parse(JSON.stringify(properties).replace(/"\$ref":"#/g, `"$ref":"${this.id}#`)),
      required,
      additionalProperties: false,
    }));
  }

  /** The response body validator for status, or null when the contract gives that status no JSON body. */
  response(method: string, route: string, status: number): ValidateFunction | null {
    const op = this.operation(method, route);
    const responses = op.responses as Json;
    const key = String(status) in responses ? String(status) : `${String(status)[0]}XX` in responses ? `${String(status)[0]}XX` : null;
    if (!key) throw new Error(`${method} ${route}: status ${status} is not in the contract`);
    const res = this.deref(responses[key] as Json);
    if (!(res.content as Json | undefined)?.["application/json"]) return null;
    const raw = responses[key] as Json;
    const pointer =
      typeof raw.$ref === "string"
        ? `${raw.$ref.replace(/^#/, "")}/content/application~1json/schema`
        : `/paths/${escape(route)}/${method.toLowerCase()}/responses/${key}/content/application~1json/schema`;
    return this.ref(pointer);
  }
}

export function errorsOf(v: ValidateFunction): string {
  return JSON.stringify(v.errors);
}
