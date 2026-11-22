/**
 * A project on the compute-box and the operations it exposes.
 *
 * Everything underneath is plain HTTP + JSON:
 *
 *   PUT  /v1/projects/{project}/operations                sync every operation, one call
 *   POST /v1/projects/{project}/operations/{name}/jobs    run one, progress streamed as SSE
 *   GET  /v1/machines                                     which local-agents are online
 */
import { z } from "zod";
import { SonicError } from "./errors.js";
import { events } from "./sse.js";

export interface Program {
  description: string;
  instructions?: string;
}

export interface InstalledProgram {
  executable: string;
  args?: string[];
  working_directory?: string;
  window_title_contains: string;
  startup_timeout_ms?: number;
}

export interface MachineOptions {
  name: string;
  programs: Record<string, InstalledProgram>;
  url?: string;
  apiKey?: string;
}

export interface SonicOptions {
  /** Project name on the compute-box. Operation names only need to be unique within it. */
  project: string;
  /** Local-agent that jobs run on by default. */
  machine?: string;
  /** Added to every operation's instructions, for things they all need (e.g. how to log in). */
  context?: string;
  /** Omit to preserve existing configuration; {} explicitly clears it. */
  programs?: Record<string, Program>;
  /** Defaults to the SONIC_URL environment variable, then http://localhost:8080. */
  url?: string;
  /** Defaults to the SONIC_API_KEY environment variable. */
  apiKey?: string;
}

export interface OperationSpec<I extends z.ZodObject, O extends z.ZodType> {
  name: string;
  description: string;
  /** Plain-language instructions for the agent. Nobody scripts the clicks. */
  instructions: string;
  input: I;
  output: O;
}

export interface Step {
  n: number;
  description: string;
}

export interface RunOptions {
  machine?: string;
  /** Retrying with the same key returns the original job instead of running it again. */
  idempotencyKey?: string;
  onStep?: (step: Step) => void;
}

export interface Definition {
  name: string;
  description: string;
  instructions: string;
  input: unknown;
  output: unknown;
}

/** An operation is an async function: typed input in, typed output out. */
export type Operation<I extends z.ZodObject, O extends z.ZodType> = ((
  input: z.input<I>,
  options?: RunOptions,
) => Promise<z.output<O>>) & { readonly definition: Definition };

export class Sonic {
  readonly project: string;
  readonly machine?: string;
  readonly context: string;
  readonly programs?: Record<string, Program>;
  readonly url: string;
  private readonly apiKey?: string;
  private readonly operations = new Map<string, Definition>();

  constructor(options: SonicOptions) {
    this.project = options.project;
    this.machine = options.machine;
    this.context = (options.context ?? "").trim();
    this.programs = options.programs;
    this.url = (options.url ?? process.env.SONIC_URL ?? "http://localhost:8080").replace(/\/$/, "");
    this.apiKey = options.apiKey ?? process.env.SONIC_API_KEY;
  }

  /** Declare an operation. Returns an async function you call like any other. */
  operation<I extends z.ZodObject, O extends z.ZodType>(spec: OperationSpec<I, O>): Operation<I, O> {
    if (this.operations.has(spec.name)) {
      throw new Error(`operation ${spec.name} is declared twice in ${this.project}`);
    }
    const definition: Definition = {
      name: spec.name,
      description: spec.description,
      instructions: [this.context, spec.instructions.trim()].filter(Boolean).join("\n\n"),
      input: z.toJSONSchema(spec.input),
      output: z.toJSONSchema(spec.output),
    };
    this.operations.set(spec.name, definition);

    const run = async (input: z.input<I>, options: RunOptions = {}): Promise<z.output<O>> => {
      const parsed = spec.input.safeParse(input);
      if (!parsed.success) throw new SonicError("invalid_input", `${spec.name}: ${parsed.error.message}`);
      const { output, jobId } = await this.runJob(spec.name, parsed.data, options);
      const result = spec.output.safeParse(output);
      if (!result.success) throw new SonicError("output_invalid", `${spec.name}: ${result.error.message}`, jobId);
      return result.data;
    };
    return Object.assign(run, { definition });
  }

  definitions(): Definition[] {
    return [...this.operations.values()];
  }

  /**
   * Make the project's operations on the compute-box match the ones declared here.
   * Syncing nothing would delete every operation in the project, so it is refused
   * unless `allowEmpty` is set; it usually means the declaring module was never imported.
   */
  async sync({ allowEmpty = false } = {}): Promise<Record<string, string[]>> {
    if (!this.operations.size && !allowEmpty) {
      throw new SonicError("invalid_input", `no operations declared in ${this.project}; refusing to sync`);
    }
    if (this.programs !== undefined) {
      await this.request("PUT", `/v1/projects/${this.project}/programs`, this.programs);
    }
    const r = await this.request("PUT", `/v1/projects/${this.project}/operations`, this.definitions());
    return r.json();
  }

  async machines(): Promise<{ name: string; online: boolean }[]> {
    const r = await this.request("GET", "/v1/machines");
    return (await r.json()).machines;
  }

  private async runJob(name: string, input: unknown, options: RunOptions) {
    const machine = options.machine ?? this.machine;
    if (!machine) throw new SonicError("invalid_input", `${name}: no machine given and no default set`);
    const headers: Record<string, string> = { Accept: "text/event-stream" };
    if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;

    const r = await this.request("POST", `/v1/projects/${this.project}/operations/${name}/jobs`, { machine, input }, headers);
    let jobId: string | undefined;
    try {
      for await (const [event, data] of events(r.body!)) {
        jobId = data.job_id ?? jobId;
        if (event === "step") options.onStep?.({ n: data.n, description: data.description });
        else if (event === "succeeded") return { output: data.output as unknown, jobId };
        else if (event === "failed" || event === "cancelled") {
          const err = data.error ?? { code: event, message: `job ${event}` };
          throw new SonicError(err.code, err.message, jobId);
        }
      }
    } catch (e) {
      if (e instanceof SonicError) throw e;
      throw new SonicError("unreachable", `compute-box at ${this.url}: ${e}`, jobId);
    }
    throw new SonicError("machine_disconnected", "the job's stream ended before it finished", jobId);
  }

  protected async request(method: string, path: string, body?: unknown, extraHeaders: Record<string, string> = {}) {
    const headers: Record<string, string> = { ...extraHeaders };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (this.apiKey) headers.Authorization = `Bearer ${this.apiKey}`;
    let r: Response;
    try {
      r = await fetch(this.url + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    } catch (e) {
      throw new SonicError("unreachable", `compute-box at ${this.url}: ${e}`);
    }
    if (!r.ok) throw await SonicError.fromResponse(r);
    return r;
  }
}

/** Independent machine installation configuration; syncing it never starts programs. */
export class Machine {
  readonly name: string;
  readonly programs: Record<string, InstalledProgram>;
  private readonly client: MachineClient;

  constructor(options: MachineOptions) {
    this.name = options.name;
    this.programs = options.programs;
    this.client = new MachineClient({ project: "", url: options.url, apiKey: options.apiKey });
  }

  async sync({ allowEmpty = false } = {}): Promise<void> {
    if (!Object.keys(this.programs).length && !allowEmpty) {
      throw new SonicError("invalid_input", "no machine programs declared; use allowEmpty to clear");
    }
    await this.client.syncMachine(this.name, this.programs);
  }
}

class MachineClient extends Sonic {
  async syncMachine(name: string, programs: Record<string, InstalledProgram>) {
    await this.request("PUT", `/v1/machines/${name}/programs`, programs);
  }
}
