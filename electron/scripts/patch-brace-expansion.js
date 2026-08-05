'use strict';

const fs = require('node:fs');
const path = require('node:path');

const packageDirectories = [];
function findPackages(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    if (!entry.isDirectory()) {
      continue;
    }
    const entryPath = path.join(directory, entry.name);
    if (entry.name === 'brace-expansion' && path.basename(directory) === 'node_modules') {
      packageDirectories.push(entryPath);
      continue;
    }
    findPackages(entryPath);
  }
}

findPackages(path.join(__dirname, '..', 'node_modules'));
if (packageDirectories.length === 0) {
  throw new Error('brace-expansion is not installed');
}

const marker = '// CommonJS compatibility for minimatch <= 5.';
const esmMarker = '// ESM default export compatibility for minimatch@9.';
for (const packageDirectory of packageDirectories) {
  const packageMetadata = require(path.join(packageDirectory, 'package.json'));
  if (packageMetadata.version !== '5.0.8') {
    throw new Error(`unsupported brace-expansion version: ${packageMetadata.version}`);
  }

  const entryPath = path.join(packageDirectory, 'dist', 'commonjs', 'index.js');
  const source = fs.readFileSync(entryPath, 'utf8');
  if (!source.includes(marker)) {
    fs.appendFileSync(
      entryPath,
      [
        '',
        marker,
        'const expansionMaxCompat = exports.EXPANSION_MAX;',
        'const expansionMaxLengthCompat = exports.EXPANSION_MAX_LENGTH;',
        'module.exports = expand;',
        'module.exports.expand = expand;',
        'module.exports.EXPANSION_MAX = expansionMaxCompat;',
        'module.exports.EXPANSION_MAX_LENGTH = expansionMaxLengthCompat;',
        '',
      ].join('\n'),
    );
  }

  const esmEntryPath = path.join(packageDirectory, 'dist', 'esm', 'index.js');
  const esmSource = fs.readFileSync(esmEntryPath, 'utf8');
  if (!esmSource.includes(esmMarker)) {
    fs.appendFileSync(esmEntryPath, ['', esmMarker, 'export default expand;', ''].join('\n'));
  }
}
