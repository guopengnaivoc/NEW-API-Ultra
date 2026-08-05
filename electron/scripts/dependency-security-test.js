'use strict';

const assert = require('node:assert/strict');
const { createRequire } = require('node:module');
const path = require('node:path');
const { pathToFileURL } = require('node:url');

const consumers = ['@electron/asar', '@electron/universal', 'filelist'];

for (const consumer of consumers) {
  const consumerRequire = createRequire(require.resolve(consumer));
  assert.equal(consumerRequire('brace-expansion/package.json').version, '5.0.8');
  const expansion = consumerRequire('brace-expansion');
  assert.equal(typeof expansion, 'function', `${consumer} requires the legacy callable API`);
  assert.equal(typeof expansion.expand, 'function', `${consumer} requires the patched named API`);
  assert.deepEqual(expansion('artifact-{linux,mac}.zip'), [
    'artifact-linux.zip',
    'artifact-mac.zip',
  ]);
}

const guardedExpansion = createRequire(require.resolve('@electron/asar'))('brace-expansion');
const bounded = guardedExpansion('{a,b}'.repeat(30), { max: 1_000, maxLength: 100 });
assert.ok(
  bounded.reduce((length, value) => length + value.length, 0) <= 100,
  'patched expansion must honor the aggregate output bound',
);

(async () => {
  const universalRequire = createRequire(require.resolve('@electron/universal'));
  const minimatchPackagePath = universalRequire.resolve('minimatch/package.json');
  const minimatchMetadata = require(minimatchPackagePath);
  assert.equal(minimatchMetadata.version, '9.0.9');

  const minimatchEntryPath = path.join(path.dirname(minimatchPackagePath), 'dist', 'esm', 'index.js');
  const minimatchEsm = await import(pathToFileURL(minimatchEntryPath).href);
  const platformArtifactPattern = 'artifacts/{linux,macos}/new-api';
  assert.equal(minimatchEsm.minimatch('artifacts/linux/new-api', platformArtifactPattern), true);
  assert.equal(minimatchEsm.minimatch('artifacts/macos/new-api', platformArtifactPattern), true);
  assert.equal(minimatchEsm.minimatch('artifacts/windows/new-api.exe', platformArtifactPattern), false);

  const minimatchRequire = createRequire(minimatchPackagePath);
  const braceExpansionPackagePath = minimatchRequire.resolve('brace-expansion/package.json');
  const braceExpansionEntryPath = path.join(
    path.dirname(braceExpansionPackagePath),
    'dist',
    'esm',
    'index.js',
  );
  const braceExpansionEsm = await import(pathToFileURL(braceExpansionEntryPath).href);
  assert.equal(braceExpansionEsm.default, braceExpansionEsm.expand);
  assert.deepEqual(braceExpansionEsm.default('artifact-{linux,mac}.zip'), [
    'artifact-linux.zip',
    'artifact-mac.zip',
  ]);
  assert.deepEqual(braceExpansionEsm.expand('artifact-{linux,mac}.zip'), [
    'artifact-linux.zip',
    'artifact-mac.zip',
  ]);

  const esmBounded = braceExpansionEsm.default('{a,b}'.repeat(30), {
    max: 1_000,
    maxLength: 100,
  });
  assert.ok(
    esmBounded.reduce((length, value) => length + value.length, 0) <= 100,
    'patched ESM expansion must honor the aggregate output bound',
  );

  console.log('patched brace-expansion compatibility: ok');
})();
