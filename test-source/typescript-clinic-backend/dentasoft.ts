/**
 * DentaSoft, the clinic's 1998 records program, as typed async functions.
 *
 * DentaSoft has no API. Each operation below is carried out by an agent that looks at the
 * screen of the clinic's PC and uses the mouse and keyboard. The input and output schemas
 * are the contract; the instructions tell the agent what to do.
 */
import { z } from "zod";
import { Sonic } from "sonic-screwdriver";

// This app's project on the compute-box. SONIC_URL and SONIC_API_KEY set the connection.
export const dentalClinic = new Sonic({
  project: "demo-project-ts",
  machine: process.env.DENTASOFT_MACHINE ?? "demo-machine",
  programs: { dentasoft: { description: "Patient records, appointments and balances." } },
  context:
    "Use DentaSoft Patient Manager; open the dentasoft program if needed. If its login box is showing, log in with " +
    "user name drsmith and password molar123. DentaSoft is slow: after each action wait for " +
    "'Please wait...' to clear. Its text boxes only support typing and Backspace: shortcuts like Ctrl+A, Home or Delete do nothing.",
});

const PatientId = z.string().regex(/^P-\d{4}$/).describe("DentaSoft patient ID, e.g. P-1004");

export const Patient = z.object({
  found: z.boolean(),
  firstName: z.string().optional(),
  lastName: z.string().optional(),
  dateOfBirth: z.string().optional().describe("MM/DD/YYYY as shown"),
  phone: z.string().optional(),
  insurance: z.string().optional(),
  balance: z.number().optional().describe("Balance Due in dollars"),
  nextAppointment: z.string().optional().describe("MM/DD/YYYY, empty if none"),
  allergies: z.string().optional(),
});
export type Patient = z.infer<typeof Patient>;

export const BalanceChange = z.object({
  previousBalance: z.number(),
  newBalance: z.number(),
});
export type BalanceChange = z.infer<typeof BalanceChange>;

export const lookupPatient = dentalClinic.operation({
  name: "lookup_patient",
  description: "Look up a patient's record by patient ID.",
  instructions: `
    Type the patient ID into the Patient ID box in the Find Patient panel and press Find.
    Click the matching row under Results, then read the fields in the Patient Record panel,
    zooming in if any text is hard to read. If DentaSoft says no records were found,
    return found=false and leave the other fields empty.`,
  input: z.object({ patientId: PatientId }),
  output: Patient,
});

export const updateBalance = dentalClinic.operation({
  name: "update_balance",
  description: "Set a patient's Balance Due.",
  instructions: `
    Find the patient by Patient ID and open their record. Note the current Balance Due.
    Click Edit, clear the Balance Due field, type the new amount with two decimals, click
    Save, answer Yes to the confirmation and OK to the success message. Then re-read
    Balance Due and only report success if it shows the new amount. If the patient does
    not exist, fail with code not_found.`,
  input: z.object({ patientId: PatientId, balance: z.number().min(0) }),
  output: BalanceChange,
});
