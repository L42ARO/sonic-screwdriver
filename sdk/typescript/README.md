# sonic-screwdriver (TypeScript SDK)

## Project programs and machine installations

```ts
import { Sonic, Machine } from "sonic-screwdriver";

const frontDesk = new Machine({
  name: "dentist-front-desk",
  programs: {
    dentasoft: {
      executable: String.raw`C:\Program Files\DentaSoft\DentaSoft.exe`,
      working_directory: String.raw`C:\Program Files\DentaSoft`,
      window_title_contains: "DentaSoft",
      args: [],
      startup_timeout_ms: 30_000,
    },
  },
});
const clinic = new Sonic({
  project: "dental-clinic",
  machine: frontDesk.name,
  context: "Use DentaSoft for patient records.",
  programs: {
    dentasoft: {
      description: "Patient records, appointments and balances.",
      instructions: "Open when patient information is needed.",
    },
  },
});
// Declare operations as usual, then:
await frontDesk.sync();
await clinic.sync();
```

The compute-box stores both registries. Machine sync replaces all installations on that
machine; configure it once for all projects sharing it. An empty machine map requires
`sync({ allowEmpty: true })`. Omitting project `programs` preserves existing configuration;
`programs: {}` explicitly clears it. Project program and operation syncs are separate
requests. Both constructors accept `url` and `apiKey` with the usual environment defaults.
Installation field names use the wire format shown above. Paths refer to the named
Windows machine. During jobs the AI chooses which registered programs to open and when;
sync does not launch them.

## Operations

Turn the tasks of a legacy program into typed async functions, with Zod schemas.

```ts
import { z } from "zod";
import { Sonic } from "sonic-screwdriver";

const dentasoft = new Sonic({ project: "dental-clinic", machine: "dentist-front-desk" });

export const updateBalance = dentasoft.operation({
  name: "update_balance",
  description: "Set a patient's Balance Due.",
  instructions: "Find the patient by Patient ID, click Edit, replace Balance Due, Save, answer Yes.",
  input: z.object({ patientId: z.string(), balance: z.number().min(0) }),
  output: z.object({ previousBalance: z.number(), newBalance: z.number() }),
});

await dentasoft.sync();                                                // once, at startup
const change = await updateBalance({ patientId: "P-1004", balance: 0 }); // typed result
```

- `sync()` makes the project's operations on the compute-box match the ones declared, in
  one call. It refuses to sync an empty project.
- Calls validate input before sending and output before returning, and throw
  `SonicError` with a stable `code` on failure.
- Per-call options go in a second argument: `{ idempotencyKey, machine, onStep }`.
- `SONIC_URL` and `SONIC_API_KEY` environment variables configure the connection. Without
  `SONIC_URL`, the SDK uses the hosted compute-box at `https://app.sonicscrew.click`; set it (for
  example `http://localhost:8080`) to use your own.
- Zod 4 is a peer dependency, so the SDK uses the app's own copy.

Build: `npm install && npm run build`. Apps in this repo install it with
`"sonic-screwdriver": "file:../../sdk/typescript"` and `install-links=true` in `.npmrc`.
