// Command morse-dsp encodes text into Morse code and CW audio and decodes
// Morse text or CW audio (WAV files or raw PCM streams) back into text.
//
//	morse-dsp encode "CQ CQ DE UR5ABC"                      # A: text → Morse text
//	morse-dsp decode --in-format text "-.-. --.-"           # B: Morse text → text
//	morse-dsp encode --out-format wav -o cq.wav "CQ"        # C: text → WAV
//	morse-dsp encode --out-format pcm "CQ" | aplay -f S16_LE -r 8000
//	morse-dsp decode -i cq.wav                              # D: WAV → text
//	python sdr.py | morse-dsp decode --in-format pcm --rate 12000
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
