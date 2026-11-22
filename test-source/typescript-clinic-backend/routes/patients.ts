/** Patient endpoints of the clinic portal, backed by DentaSoft. */
import { Router } from "express";
import { lookupPatient, updateBalance } from "../dentasoft.ts";

export const patients = Router();

patients.get("/:id", async (req, res) => {
  const patient = await lookupPatient({ patientId: req.params.id });
  if (!patient.found) {
    res.status(404).json({ code: "not_found", message: `no patient ${req.params.id}` });
    return;
  }
  res.json(patient);
});

patients.put("/:id/balance", async (req, res) => {
  const change = await updateBalance(
    { patientId: req.params.id, balance: req.body.balance },
    // Lets callers retry safely: the same requestId never edits DentaSoft twice.
    { idempotencyKey: req.body.requestId },
  );
  res.json(change);
});
