import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/game.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { streamPod, evaluate } = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);
const vectors = JSON.parse(await readFile(new URL('../../testdata/pod-stream.json', import.meta.url), 'utf8'));
const templates = ['Envoy', 'Prometheus', 'OpenTelemetry', 'Jaeger', 'Fluent Bit', 'CoreDNS'].map(name => ({ name }));

test('Pod sequence matches shared server vectors', () => {
  for (const vector of vectors) {
    for (const expected of vector.pods) {
      const actual = streamPod({ seed: vector.seed, templates }, expected.index);
      assert.equal(actual.id, `pod-${expected.index + 1}`);
      assert.equal(actual.name, expected.name);
      assert.equal(actual.cpu, expected.cpu);
      assert.equal(actual.memory, expected.memory);
    }
  }
});

test('local score matches server vectors including a replacement', async () => {
  const vectors = JSON.parse(await readFile(new URL('../../testdata/score.json', import.meta.url), 'utf8'));
  const game = {
    nodes: [{id:'node-a',cpu:8,memory:16,zone:'east'}, {id:'node-b',cpu:8,memory:16,zone:'west'}, {id:'node-c',cpu:8,memory:16,zone:'east'}],
    templates,
    recipes: [
      {id:'tracing',projects:['OpenTelemetry','Jaeger'],points:800},
      {id:'metrics',projects:['OpenTelemetry','Prometheus'],points:800},
      {id:'proxy-monitoring',projects:['Envoy','Prometheus'],points:600},
      {id:'dns-monitoring',projects:['CoreDNS','Prometheus'],points:600},
      {id:'logs',projects:['Fluent Bit','OpenTelemetry'],points:600},
    ],
  };
  for (const vector of vectors) {
    const result = evaluate({...game,seed:vector.seed},vector.actions);
    assert.equal(result.points,vector.score,vector.name);
    assert.equal(result.placed.size,vector.running,vector.name);
  }
});
