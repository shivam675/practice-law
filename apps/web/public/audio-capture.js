class Capture extends AudioWorkletProcessor {
  constructor() { super(); this.samples = []; }
  process(inputs) {
    const channel = inputs[0]?.[0];
    if (channel) {
      for (const value of channel) this.samples.push(Math.max(-1, Math.min(1, value)) * 32767);
      if (this.samples.length >= 1600) {
        const pcm = Int16Array.from(this.samples.splice(0, 1600));
        this.port.postMessage(pcm.buffer, [pcm.buffer]);
      }
    }
    return true;
  }
}
registerProcessor("capture", Capture);
