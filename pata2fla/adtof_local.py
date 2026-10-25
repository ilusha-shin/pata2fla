#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Optional LOCAL ADTOF backend. drum-pipeline.exe calls this script only when models/adtof.onnx
is missing (or when started with --adtof=local) and Python with adtof_pytorch is installed:

    python adtof_local.py --audio drums.wav --out onsets.mid

It runs the installed ADTOF-pytorch (adtof_pytorch.transcribe_to_midi) on the isolated drum stem.
The Go program reads the resulting MIDI file itself.
"""
import argparse
import sys


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--audio", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    try:
        from adtof_pytorch import transcribe_to_midi
    except Exception as e:  # ImportError, or a broken torch install
        print("[ERROR] cannot import adtof_pytorch: %s" % e)
        print("        pip install git+https://github.com/xavriley/ADTOF-pytorch.git")
        return 2
    try:
        transcribe_to_midi(args.audio, args.out)
    except Exception as e:
        print("[ERROR] adtof_pytorch.transcribe_to_midi failed: %s" % e)
        return 1
    print("[OK] ADTOF (local, PyTorch) finished: %s" % args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
