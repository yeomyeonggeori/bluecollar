// Package tape records the model traffic of a turn and plays it back without a model.
//
// A tape is for two things and neither of them is acceptance. It gives the loop's own
// guarantees real inputs, including the malformed ones nobody would think to hand-write,
// and it lets a run that went wrong be walked again for nothing. Whether the agent works
// is measured against a live model by bench, with that benchmark's own verifier; a
// recorded answer replayed as proof of behaviour would be worth less than nothing.
package tape
