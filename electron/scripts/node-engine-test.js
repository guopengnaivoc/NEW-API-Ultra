'use strict';

const assert = require('node:assert/strict');
const packageLock = require('../package-lock.json');
const packageManifest = require('../package.json');
const semver = require('semver');

const manifestRange = packageManifest.engines?.node;
const lockRange = packageLock.packages?.['']?.engines?.node;

assert.equal(lockRange, manifestRange, 'package.json and package-lock.json must declare the same Node range');

const declaredMinimum = semver.minVersion(manifestRange);
assert.ok(declaredMinimum, `Node engine range must have a minimum version: ${manifestRange}`);

const incompatiblePackages = Object.entries(packageLock.packages)
  .filter(([, packageMetadata]) => packageMetadata?.engines?.node)
  .filter(([, packageMetadata]) => !semver.satisfies(declaredMinimum, packageMetadata.engines.node))
  .map(([packagePath, packageMetadata]) =>
    `${packagePath}@${packageMetadata.version} requires Node ${packageMetadata.engines.node}`,
  );

assert.deepEqual(
  incompatiblePackages,
  [],
  `declared Node floor ${declaredMinimum.version} is incompatible with locked packages:\n${incompatiblePackages.join('\n')}`,
);

assert.ok(
  semver.satisfies(process.version, manifestRange),
  `executing Node ${process.version} does not satisfy declared range ${manifestRange}`,
);

console.log(`Node engine contract: ${manifestRange}`);
