#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
Export ADTOF-pytorch to ONNX and validate PyTorch vs ONNX Runtime.

Requirements:
    pip install onnx onnxruntime

ADTOF-pytorch:
    pip install -e path/to/ADTOF-pytorch

Output:
    adtof.onnx
    adtof_meta.json
"""

import json
import sys
from pathlib import Path

import numpy as np
import torch


# ============================================================
# 1. Проверяем зависимости
# ============================================================

try:
    import onnx
except ImportError:
    print("ERROR: onnx is not installed.")
    print("Run:")
    print("    pip install onnx")
    sys.exit(1)

try:
    import onnxruntime as ort
except ImportError:
    print("ERROR: onnxruntime is not installed.")
    print("Run:")
    print("    pip install onnxruntime")
    sys.exit(1)


# ============================================================
# 2. Импорт ADTOF
# ============================================================

try:
    from adtof_pytorch import (
        create_frame_rnn_model,
        calculate_n_bins,
        load_pytorch_weights,
        get_default_weights_path,
    )
except ImportError as e:
    print("ERROR: Cannot import adtof_pytorch.")
    print()
    print("Install ADTOF-pytorch first:")
    print("    pip install -e path/to/ADTOF-pytorch")
    print()
    print("Original error:")
    print(e)
    sys.exit(1)


# ============================================================
# Configuration
# ============================================================

OUTPUT_ONNX = "adtof.onnx"
OUTPUT_META = "adtof_meta.json"

OPSET_VERSION = 17

# ADTOF uses:
# 44100 Hz
# 100 FPS
# 2048 FFT
# 12 bands/octave
# 20 Hz - 20000 Hz

SAMPLE_RATE = 44100
FPS = 100
FRAME_SIZE = 2048
BANDS_PER_OCTAVE = 12
FMIN = 20.0
FMAX = 20000.0

CLASS_NAMES = [
    "kick",
    "snare",
    "closed_hat",
    "open_hat",
    "tom_or_cymbal",
]


# ============================================================
# Helper
# ============================================================

def print_model_info(model):
    print()
    print("========== MODEL ==========")
    print(model)

    if hasattr(model, "get_model_info"):
        info = model.get_model_info()

        print()
        print("Model info:")
        for key, value in info.items():
            print(f"  {key}: {value}")

    print("===========================")
    print()


# ============================================================
# Main
# ============================================================

def main():

    print("==============================================")
    print("       ADTOF PyTorch -> ONNX exporter")
    print("==============================================")
    print()

    # --------------------------------------------------------
    # Check PyTorch
    # --------------------------------------------------------

    print("[1/7] PyTorch")
    print(f"      version: {torch.__version__}")

    device = torch.device("cpu")

    print("      device: CPU")
    print()

    # --------------------------------------------------------
    # Calculate number of mel/log-frequency bins
    # --------------------------------------------------------

    print("[2/7] Calculating ADTOF input dimensions...")

    n_bins = calculate_n_bins(
        bands_per_octave=BANDS_PER_OCTAVE,
        fmin=FMIN,
        fmax=FMAX,
        frame_size=FRAME_SIZE,
        sample_rate=SAMPLE_RATE,
    )

    print(f"      n_bins = {n_bins}")
    print(f"      FPS    = {FPS}")
    print()

    # --------------------------------------------------------
    # Create model
    # --------------------------------------------------------

    print("[3/7] Creating ADTOF model...")

    model = create_frame_rnn_model(n_bins)
    model.eval()
    model.to(device)

    print_model_info(model)

    # --------------------------------------------------------
    # Find bundled weights
    # --------------------------------------------------------

    print("[4/7] Loading pretrained weights...")

    weights_path = get_default_weights_path()

    if weights_path is None:
        print()
        print("ERROR: ADTOF default weights path was not found.")
        print()
        print("Expected bundled file:")
        print("    adtof_pytorch/data/adtof_frame_rnn_pytorch_weights.pth")
        print()
        sys.exit(1)

    weights_path = Path(weights_path)

    print(f"      weights: {weights_path}")

    if not weights_path.exists():
        print()
        print("ERROR: Weight file does not exist:")
        print(weights_path)
        sys.exit(1)

    model = load_pytorch_weights(
        model,
        str(weights_path),
        strict=False,
    )

    model.eval()
    model.to(device)

    print("      weights loaded")
    print()

    # --------------------------------------------------------
    # Dummy input
    # --------------------------------------------------------

    print("[5/7] Creating test input...")

    #
    # IMPORTANT:
    #
    # ADTOF model.forward() expects:
    #
    #   [batch, time, frequency, channels]
    #
    # Example:
    #
    #   [1, 100, 84, 1]
    #
    # The exact number of frequency bins is calculated above.
    #

    TEST_FRAMES = 100

    dummy_input = torch.randn(
        1,
        TEST_FRAMES,
        n_bins,
        1,
        dtype=torch.float32,
        device=device,
    )

    print(f"      input shape: {tuple(dummy_input.shape)}")

    # --------------------------------------------------------
    # PyTorch reference inference
    # --------------------------------------------------------

    print()
    print("      Running PyTorch inference...")

    with torch.no_grad():
        pytorch_output = model(dummy_input)

    pytorch_output_np = (
        pytorch_output
        .detach()
        .cpu()
        .numpy()
        .astype(np.float32)
    )

    print(f"      output shape: {pytorch_output_np.shape}")

    # --------------------------------------------------------
    # Export ONNX
    # --------------------------------------------------------

    print()
    print("      Exporting ONNX...")

    torch.onnx.export(
        model,
        dummy_input,
        OUTPUT_ONNX,

        export_params=True,

        opset_version=OPSET_VERSION,

        do_constant_folding=True,

        input_names=[
            "input"
        ],

        output_names=[
            "output"
        ],

        dynamic_axes={
            "input": {
                0: "batch",
                1: "time",
            },

            "output": {
                0: "batch",
                1: "time",
            },
        },
    )

    print(f"      ONNX saved: {Path(OUTPUT_ONNX).absolute()}")
    print()

    # --------------------------------------------------------
    # Validate ONNX file
    # --------------------------------------------------------

    print("[6/7] Validating ONNX model...")

    onnx_model = onnx.load(OUTPUT_ONNX)

    onnx.checker.check_model(onnx_model)

    print("      ONNX checker: OK")

    # --------------------------------------------------------
    # ONNX Runtime
    # --------------------------------------------------------

    print()
    print("      Creating ONNX Runtime session...")

    available_providers = ort.get_available_providers()

    print("      Available providers:")
    for provider in available_providers:
        print(f"        - {provider}")

    session = ort.InferenceSession(
        OUTPUT_ONNX,
        providers=[
            "CPUExecutionProvider"
        ],
    )

    input_info = session.get_inputs()[0]
    output_info = session.get_outputs()[0]

    print()
    print("      ONNX input:")
    print(f"        name : {input_info.name}")
    print(f"        shape: {input_info.shape}")
    print(f"        type : {input_info.type}")

    print()
    print("      ONNX output:")
    print(f"        name : {output_info.name}")
    print(f"        shape: {output_info.shape}")
    print(f"        type : {output_info.type}")

    # --------------------------------------------------------
    # Run ONNX
    # --------------------------------------------------------

    print()
    print("      Running ONNX Runtime inference...")

    onnx_output = session.run(
        [output_info.name],
        {
            input_info.name: dummy_input.cpu().numpy()
        },
    )[0]

    onnx_output = np.asarray(
        onnx_output,
        dtype=np.float32,
    )

    print(f"      output shape: {onnx_output.shape}")

    # --------------------------------------------------------
    # Compare PyTorch and ONNX
    # --------------------------------------------------------

    print()
    print("==============================================")
    print("       PyTorch vs ONNX")
    print("==============================================")

    if pytorch_output_np.shape != onnx_output.shape:

        print()
        print("ERROR: Output shapes differ!")
        print()
        print(f"PyTorch: {pytorch_output_np.shape}")
        print(f"ONNX:    {onnx_output.shape}")
        print()

        sys.exit(1)

    difference = np.abs(
        pytorch_output_np - onnx_output
    )

    max_difference = float(
        difference.max()
    )

    mean_difference = float(
        difference.mean()
    )

    rmse = float(
        np.sqrt(
            np.mean(
                np.square(
                    pytorch_output_np - onnx_output
                )
            )
        )
    )

    print()
    print(f"Output shape       : {pytorch_output_np.shape}")
    print(f"Maximum difference : {max_difference:.10f}")
    print(f"Mean difference    : {mean_difference:.10f}")
    print(f"RMSE               : {rmse:.10f}")

    # --------------------------------------------------------
    # Determine pass/fail
    # --------------------------------------------------------

    print()

    if max_difference < 1e-4:

        print("PASS")
        print("ONNX output matches PyTorch.")

    elif max_difference < 1e-3:

        print("PASS (small numerical difference)")
        print("ONNX output is very close to PyTorch.")

    else:

        print("WARNING")
        print("ONNX and PyTorch outputs differ more than expected.")

    # --------------------------------------------------------
    # Metadata
    # --------------------------------------------------------

    metadata = {
        "model": "ADTOF-pytorch",
        "format": "ONNX",

        "onnx_opset": OPSET_VERSION,

        "sample_rate": SAMPLE_RATE,
        "fps": FPS,
        "frame_size": FRAME_SIZE,

        "bands_per_octave": BANDS_PER_OCTAVE,

        "fmin": FMIN,
        "fmax": FMAX,

        "n_bins": int(n_bins),

        "input": {
            "name": "input",
            "shape": [
                "batch",
                "time",
                int(n_bins),
                1
            ],
            "dtype": "float32"
        },

        "output": {
            "name": "output",
            "shape": [
                "batch",
                "time",
                5
            ],
            "dtype": "float32"
        },

        "classes": CLASS_NAMES,

        "weights": str(weights_path),

        "validation": {
            "max_difference": max_difference,
            "mean_difference": mean_difference,
            "rmse": rmse
        }
    }

    with open(
        OUTPUT_META,
        "w",
        encoding="utf-8",
    ) as f:

        json.dump(
            metadata,
            f,
            indent=2,
            ensure_ascii=False,
        )

    print()
    print("==============================================")
    print("              DONE")
    print("==============================================")
    print()
    print(f"ONNX : {Path(OUTPUT_ONNX).absolute()}")
    print(f"META : {Path(OUTPUT_META).absolute()}")
    print()
    print("The ONNX model can now be loaded by")
    print("ONNX Runtime from Go.")
    print()


if __name__ == "__main__":
    main()