import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

const root = fileURLToPath(new URL('../', import.meta.url));
const staged = process.argv.includes('--staged');
const schemaPath = 'subscriptions/schema.json';
const catalogPath = 'subscriptions/mirrors.json';

function readJson(path) {
	try {
		const content = staged
			? execFileSync('git', ['show', `:${path}`], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
			: readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
		return JSON.parse(content);
	} catch (error) {
		throw new Error(`${path}: ${error.message}`);
	}
}

try {
	const ajv = new Ajv2020({ allErrors: true });
	addFormats(ajv);
	let validate;
	try {
		validate = ajv.compile(readJson(schemaPath));
	} catch (error) {
		throw new Error(`${schemaPath}: ${error.message}`);
	}
	if (!validate(readJson(catalogPath))) {
		for (const error of validate.errors) {
			console.error(`${catalogPath}${error.instancePath || '/'}: ${ajv.errorsText([error], { dataVar: 'value' })}`);
		}
		process.exitCode = 1;
	} else {
		console.log(`${catalogPath}: Schema validation passed${staged ? ' (staged files)' : ''}.`);
	}
} catch (error) {
	console.error(error.message);
	process.exitCode = 1;
}
