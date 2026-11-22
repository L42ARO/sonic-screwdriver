/**
 * Typed TypeScript client for the sonic-screwdriver compute-box.
 *
 *   const dentasoft = new Sonic({ project: "dental-clinic", machine: "dentist-front-desk" });
 *
 *   export const lookupPatient = dentasoft.operation({
 *     name: "lookup_patient",
 *     description: "Look up a patient's record by patient ID.",
 *     instructions: "Type the ID into the Patient ID box, press Find, open the row, read the record.",
 *     input: z.object({ patientId: z.string() }),
 *     output: Patient,
 *   });
 *
 *   await dentasoft.sync();                                  // once, at startup
 *   const patient = await lookupPatient({ patientId: "P-1004" });
 */
export { Sonic, Machine } from "./client.js";
export type { Program, InstalledProgram, MachineOptions } from "./client.js";
export type { Definition, Operation, OperationSpec, RunOptions, SonicOptions, Step } from "./client.js";
export { SonicError } from "./errors.js";
