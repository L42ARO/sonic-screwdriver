/**
 * sonic-screwdriver plumbing for Express. Nothing to edit here.
 *
 * Syncs the operations declared in dentasoft.ts, adds /health, and turns agent failures
 * into HTTP errors. Call it after your routes, since Express runs error handlers last.
 */
import type { Express, NextFunction, Request, Response } from "express";
import { SonicError } from "sonic-screwdriver";
import { dentalClinic } from "../dentasoft.ts";

const STATUS: Record<string, number> = {
  invalid_input: 422,
  not_found: 404,
  unreachable: 503,
  machine_offline: 503,
  machine_busy: 503,
};

export async function setup(app: Express): Promise<void> {
  try {
    await dentalClinic.sync();
  } catch (e) {
    console.warn(`could not sync operations: ${e}`);
  }

  app.get("/health", async (_req, res) => {
    const machines = await dentalClinic.machines();
    const online = machines.find((m) => m.name === dentalClinic.machine)?.online ?? false;
    res.json({ ok: true, machineOnline: online });
  });

  app.use((err: unknown, _req: Request, res: Response, next: NextFunction) => {
    if (!(err instanceof SonicError)) return next(err);
    res.status(STATUS[err.code] ?? 502).json({ code: err.code, message: err.message });
  });
}
