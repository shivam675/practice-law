const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
let Capture;
const messages = [];
vm.runInNewContext(fs.readFileSync(__dirname + '/audio-capture.js', 'utf8'), {
  AudioWorkletProcessor: class { constructor() { this.port = { postMessage: data => messages.push(data) }; } },
  registerProcessor: (_, processor) => Capture = processor,
});
const capture = new Capture();
for (let i = 0; i < 15; i++) capture.process([[new Float32Array(128).fill(.2)]]);
capture.port.onmessage({ data: 'finish' });
assert.equal(messages.length, 2);
assert.equal(messages[0].byteLength / 2 + messages[1].audio.byteLength / 2, 15 * 128);
assert.equal(messages[1].type, 'finish');
console.log('PASS: every captured sample precedes the finish marker');
