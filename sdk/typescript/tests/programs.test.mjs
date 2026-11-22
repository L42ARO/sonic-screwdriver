import test from "node:test";
import assert from "node:assert/strict";
import { Machine, Sonic } from "../dist/index.js";

test("machine installations and project guidance sync independently", async () => {
  const original = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (url, init) => {
    requests.push([new URL(url).pathname, JSON.parse(init.body)]);
    return new Response('{}', { status: 200 });
  };
  try {
    const machine = new Machine({ name: "desk", programs: {
      records: { executable: String.raw`C:\Apps\records.exe`, args: ["a b", "x&y"], window_title_contains: "Records" },
    } });
    const project = new Sonic({ project: "clinic", machine: machine.name, programs: { records: { description: "Patient records" } } });
    await machine.sync();
    await project.sync({ allowEmpty: true });
    assert.deepEqual(requests.map(([path]) => path), ["/v1/machines/desk/programs", "/v1/projects/clinic/programs", "/v1/projects/clinic/operations"]);
    assert.deepEqual(requests[0][1].records.args, ["a b", "x&y"]);
    assert.equal(requests[1][1].records.executable, undefined);
    requests.length = 0;
    await new Sonic({ project: "old" }).sync({ allowEmpty: true });
    assert.equal(requests.length, 1);
    await assert.rejects(new Machine({ name: "desk", programs: {} }).sync(), /no machine programs/);
  } finally {
    globalThis.fetch = original;
  }
});
