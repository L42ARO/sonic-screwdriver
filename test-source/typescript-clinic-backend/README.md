# Clinic Patient Portal, TypeScript (sample caller)

The same clinic backend as the Python sample, written the way a TypeScript developer
would: Express for the server, Zod for the types. Patient records live in DentaSoft, a
1998 program with no API, reached through the sonic-screwdriver compute-box.

## Layout

```
main.ts                the Express app; one line wires in sonic
dentasoft.ts           written by the developer: the project and DentaSoft's
                       tasks as typed async functions
integrations/
  sonic.ts             sonic plumbing, never edited: startup sync, /health,
                       error handling
routes/patients.ts     GET /patients/:id, PUT /patients/:id/balance
```

## Defining an operation

```ts
export const dentalClinic = new Sonic({ project: "dental-clinic-ts", machine: "dentist-front-desk" });

export const updateBalance = dentalClinic.operation({
  name: "update_balance",
  description: "Set a patient's Balance Due.",
  instructions: `Find the patient by Patient ID, click Edit, replace Balance Due, click Save...`,
  input: z.object({ patientId: PatientId, balance: z.number().min(0) }),
  output: BalanceChange,
});
```

TypeScript comments don't exist at run time, so the instructions are a field rather than a
docstring. Calling it is an ordinary function call, fully typed from the Zod schemas:

```ts
const change = await updateBalance({ patientId: "P-1004", balance: 0 }, { idempotencyKey: "inv-5521" });
change.newBalance; // number
```

## Run

Node 24 runs TypeScript directly, so there is no build step:

```powershell
npm install
$env:SONIC_URL = "http://localhost:8080"
npm start            # node main.ts
npm run check        # type-check with tsc
```

The compute-box is not built yet, so this sample can't do real work today.
