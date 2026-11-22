/**
 * Clinic patient-portal backend. Its patient data lives in DentaSoft, reached via the agent.
 *
 *   node main.ts
 */
import express from "express";
import * as sonic from "./integrations/sonic.ts";
import { patients } from "./routes/patients.ts";

const app = express();
app.use(express.json());
app.use("/patients", patients);
await sonic.setup(app);

const port = Number(process.env.PORT ?? 8000);
app.listen(port, () => console.log(`clinic portal on http://localhost:${port}`));
