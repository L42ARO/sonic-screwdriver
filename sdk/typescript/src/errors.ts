/**
 * Anything that went wrong reaching the compute-box or running a job.
 *
 * `code` is stable and safe to branch on: unreachable, invalid_input, not_found,
 * machine_offline, machine_busy, machine_disconnected, timeout, agent_gave_up,
 * output_invalid, cancelled.
 */
export class SonicError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly jobId?: string,
  ) {
    super(`${code}: ${message}`);
    this.name = "SonicError";
  }

  /** Build from an error response: {"error": {"code": ..., "message": ...}}. */
  static async fromResponse(r: Response): Promise<SonicError> {
    const text = await r.text();
    try {
      const { error } = JSON.parse(text);
      return new SonicError(error.code, error.message);
    } catch {
      return new SonicError(`http_${r.status}`, text.slice(0, 300));
    }
  }
}
