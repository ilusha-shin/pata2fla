#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
export_to_onnx_n.py — исправленный экспорт PyTorch → ONNX.

Отличия от старого export_to_onnx.py:
  • mean / std / names / arch читаются из чекпоинта (не 0/1)
  • mel_phys: экспорт с двумя входами (mel + phys), не нули
  • models_meta.json: frontend hop=512 + phys_mean/phys_std при наличии
  • strict load где возможно

Пример:
  python export_to_onnx_n.py --models-dir "G:\\data-train\\arbiter90\\6460_3heads\\processed_refine60phys_oldhh" --out-dir "G:\\groovy-drum-pipe\\models"
"""

from __future__ import annotations

import argparse
import json
import warnings
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

import numpy as np
import torch
import torch.nn as nn

warnings.filterwarnings("ignore", category=FutureWarning)

PHYS_DIM = 6

MODEL_CONFIGS = {
    "hh_best": {
        "n_classes": 3,
        "names": ["closed_hat", "open_hat", "pedal_hat"],
        "frames": 60,
        "desc": "Hi-Hat specialist",
    },
    "cy_best": {
        "n_classes": 2,
        "names": ["ride", "crash"],
        "frames": 60,
        "desc": "Cymbals specialist",
    },
    "tt_best": {
        "n_classes": 3,
        "names": ["low_tom", "mid_tom", "high_tom"],
        "frames": 60,
        "desc": "Toms specialist",
    },
    "cy_arbiter": {
        "n_classes": 5,
        "names": ["closed_hat", "open_hat", "pedal_hat", "ride", "crash"],
        "frames": 90,
        "desc": "Iron arbiter 5-class",
    },
}


# --------------------------------------------------------------------------- models
class MelOnlyCNN(nn.Module):
    """Classic 32-64-128 → classifier; Linear keys match train (classifier.2.*)."""

    def __init__(self, n_classes: int, c1=32, c2=64, c3=128, dropout=0.35):
        super().__init__()
        self.features = nn.Sequential(
            nn.Conv2d(1, c1, 3, padding=1),
            nn.BatchNorm2d(c1),
            nn.ReLU(inplace=True),
            nn.MaxPool2d(2),
            nn.Conv2d(c1, c2, 3, padding=1),
            nn.BatchNorm2d(c2),
            nn.ReLU(inplace=True),
            nn.MaxPool2d(2),
            nn.Conv2d(c2, c3, 3, padding=1),
            nn.BatchNorm2d(c3),
            nn.ReLU(inplace=True),
            nn.AdaptiveAvgPool2d((1, 1)),
        )
        # Flatten + Dropout + Linear  →  state_dict keys classifier.2.weight/bias
        self.classifier = nn.Sequential(
            nn.Flatten(),
            nn.Dropout(dropout),
            nn.Linear(c3, n_classes),
        )

    def forward(self, mel):
        h = self.features(mel)
        return self.classifier(h)


class MelPhysCNN(nn.Module):
    """CNN mel + MLP phys → concat → Linear (as train_refine_phys / train_arbiter_phys)."""

    def __init__(self, n_classes: int, phys_dim: int = PHYS_DIM, c1=32, c2=64, c3=128):
        super().__init__()
        self.features = nn.Sequential(
            nn.Conv2d(1, c1, 3, padding=1),
            nn.BatchNorm2d(c1),
            nn.ReLU(inplace=True),
            nn.MaxPool2d(2),
            nn.Conv2d(c1, c2, 3, padding=1),
            nn.BatchNorm2d(c2),
            nn.ReLU(inplace=True),
            nn.MaxPool2d(2),
            nn.Conv2d(c2, c3, 3, padding=1),
            nn.BatchNorm2d(c3),
            nn.ReLU(inplace=True),
            nn.AdaptiveAvgPool2d((1, 1)),
        )
        self.phys_mlp = nn.Sequential(
            nn.Linear(phys_dim, 32),
            nn.ReLU(inplace=True),
            nn.Linear(32, 32),
            nn.ReLU(inplace=True),
        )
        self.classifier = nn.Sequential(
            nn.Dropout(0.3),
            nn.Linear(c3 + 32, n_classes),
        )

    def forward(self, mel, phys):
        h = self.features(mel).flatten(1)
        p = self.phys_mlp(phys)
        return self.classifier(torch.cat([h, p], dim=1))


def clean_state_dict(sd: dict) -> dict:
    out = {}
    for k, v in sd.items():
        k = k.replace("module.", "").replace("model.", "").replace("_orig_mod.", "")
        out[k] = v
    return out


def load_checkpoint(path: Path) -> dict:
    try:
        ckpt = torch.load(str(path), map_location="cpu", weights_only=False)
    except TypeError:
        ckpt = torch.load(str(path), map_location="cpu")
    if isinstance(ckpt, nn.Module):
        return {"_module": ckpt}
    if not isinstance(ckpt, dict):
        raise ValueError(f"unknown checkpoint type: {type(ckpt)}")
    return ckpt


def extract_meta(ckpt: dict, default_names: List[str], default_frames: int) -> dict:
    """Pull mean/std/names/arch/phys stats from checkpoint."""
    mean = float(ckpt.get("mean", 0.0))
    std = float(ckpt.get("std", 1.0))
    if std == 0.0:
        std = 1.0
    names = ckpt.get("names") or default_names
    if not isinstance(names, list):
        names = default_names
    arch = str(ckpt.get("arch", "mel"))
    patch_frames = int(ckpt.get("patch_frames", default_frames))
    phys_mean = ckpt.get("phys_mean")
    phys_std = ckpt.get("phys_std")
    phys_dim = int(ckpt.get("phys_dim", PHYS_DIM))
    n_classes = int(ckpt.get("n_classes", len(names)))

    # detect arch from weights if not set
    sd = ckpt.get("state_dict", ckpt.get("model_state_dict"))
    if isinstance(sd, dict):
        sd = clean_state_dict(sd)
        if any(k.startswith("phys_mlp") for k in sd):
            arch = "mel_phys"
        # classifier last linear out features
        for k in reversed(list(sd.keys())):
            if k.endswith("weight") and sd[k].ndim == 2 and ("classifier" in k or "fc" in k):
                n_classes = int(sd[k].shape[0])
                break

    return {
        "mean": mean,
        "std": std,
        "names": list(names),
        "arch": arch,
        "patch_frames": patch_frames,
        "phys_mean": phys_mean,
        "phys_std": phys_std,
        "phys_dim": phys_dim,
        "n_classes": n_classes,
    }


def infer_channels(sd: dict) -> Tuple[int, int, int]:
    c1, c2, c3 = 32, 64, 128
    if "features.0.weight" in sd:
        c1 = int(sd["features.0.weight"].shape[0])
    if "features.4.weight" in sd:
        c2 = int(sd["features.4.weight"].shape[0])
    if "features.8.weight" in sd:
        c3 = int(sd["features.8.weight"].shape[0])
    return c1, c2, c3


def build_and_load(ckpt: dict, meta: dict) -> nn.Module:
    if "_module" in ckpt:
        m = ckpt["_module"]
        m.eval()
        return m

    sd = ckpt.get("state_dict", ckpt.get("model_state_dict", ckpt))
    if not isinstance(sd, dict):
        raise ValueError("no state_dict")
    sd = clean_state_dict(sd)
    c1, c2, c3 = infer_channels(sd)
    n_classes = meta["n_classes"]
    arch = meta["arch"]

    if arch == "mel_phys" or any(k.startswith("phys_mlp") for k in sd):
        model = MelPhysCNN(n_classes, phys_dim=meta["phys_dim"], c1=c1, c2=c2, c3=c3)
        missing, unexpected = model.load_state_dict(sd, strict=False)
        print(f"   arch=mel_phys  ch={c1}->{c2}->{c3}  classes={n_classes}")
        if missing:
            print(f"   [warn] missing keys: {len(missing)}")
        if unexpected:
            print(f"   [warn] unexpected keys: {len(unexpected)}")
    else:
        model = MelOnlyCNN(n_classes, c1=c1, c2=c2, c3=c3)
        try:
            model.load_state_dict(sd, strict=True)
            print(f"   arch=mel  ch={c1}->{c2}->{c3}  classes={n_classes}  strict=OK")
        except Exception as e1:
            # remap classifier.1.* → classifier.2.* (old MelOnly without Flatten)
            sd2 = dict(sd)
            for a, b in (
                ("classifier.1.weight", "classifier.2.weight"),
                ("classifier.1.bias", "classifier.2.bias"),
            ):
                if a in sd2 and b not in sd2:
                    sd2[b] = sd2.pop(a)
            try:
                model.load_state_dict(sd2, strict=True)
                print(f"   arch=mel  ch={c1}->{c2}->{c3}  classes={n_classes}  strict=OK (remapped cls)")
            except Exception as e2:
                model.load_state_dict(sd, strict=False)
                print(f"   arch=mel  ch={c1}->{c2}->{c3}  classes={n_classes}  strict=False")
                print(f"   [warn] {e1}")

    model.eval()
    return model


def export_onnx(model: nn.Module, out_path: Path, frames: int, arch: str, phys_dim: int = PHYS_DIM):
    out_path.parent.mkdir(parents=True, exist_ok=True)
    model.eval()
    mel = torch.randn(1, 1, 64, frames, dtype=torch.float32)

    if arch == "mel_phys" and hasattr(model, "phys_mlp"):
        phys = torch.randn(1, phys_dim, dtype=torch.float32)
        torch.onnx.export(
            model,
            (mel, phys),
            str(out_path),
            export_params=True,
            opset_version=14,
            do_constant_folding=True,
            input_names=["input_mel_patch", "input_phys"],
            output_names=["logits"],
            dynamic_axes={
                "input_mel_patch": {0: "batch"},
                "input_phys": {0: "batch"},
                "logits": {0: "batch"},
            },
        )
        print(f"   ONNX: {out_path.name}  inputs=[mel 1x1x64x{frames}, phys 1x{phys_dim}]")
    else:
        torch.onnx.export(
            model,
            mel,
            str(out_path),
            export_params=True,
            opset_version=14,
            do_constant_folding=True,
            input_names=["input_mel_patch"],
            output_names=["logits"],
            dynamic_axes={
                "input_mel_patch": {0: "batch"},
                "logits": {0: "batch"},
            },
        )
        print(f"   ONNX: {out_path.name}  input=[mel 1x1x64x{frames}]")


def identify_model(path: Path) -> Optional[str]:
    n = path.name.lower()
    if "cy_arbiter" in n or ("arbiter" in n and "tt" not in n):
        return "cy_arbiter"
    if "hh_best" in n or n.startswith("hh"):
        return "hh_best"
    if "cy_best" in n or n.startswith("cy"):
        return "cy_best"
    if "tt_best" in n or n.startswith("tt"):
        return "tt_best"
    return None


def write_meta(meta_path: Path, entries: Dict[str, dict]):
    """Write models_meta.json with frontend matching Python train (hop=512)."""
    doc: Dict[str, Any] = {
        "frontend": {
            "sr": 44100,
            "n_fft": 2048,
            "hop": 512,
            "n_mels": 64,
            "fmin": 20.0,
            "fmax": 16000.0,
            "log": "db",
            "htk": False,
            "note": "Must match prepare_refine_* / train_refine_* (hop=512)",
        }
    }
    for key, m in entries.items():
        item = {
            "n_classes": m["n_classes"],
            "names": m["names"],
            "mean": m["mean"],
            "std": m["std"],
            "patch_shape": [64, m["patch_frames"]],
            "window_frames": m["patch_frames"],
            "arch": m["arch"],
        }
        if m.get("phys_mean") is not None:
            pm = m["phys_mean"]
            ps = m["phys_std"]
            item["phys_mean"] = pm if isinstance(pm, list) else list(np.asarray(pm).reshape(-1).astype(float))
            item["phys_std"] = ps if isinstance(ps, list) else list(np.asarray(ps).reshape(-1).astype(float))
            item["phys_dim"] = m.get("phys_dim", PHYS_DIM)
        doc[key] = item

    meta_path.write_text(json.dumps(doc, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"\n✓ models_meta.json → {meta_path}")


def main():
    ap = argparse.ArgumentParser(description="Export drum CNNs to ONNX (mean/std from ckpt)")
    ap.add_argument("--models-dir", required=True, help="Folder with .pt checkpoints")
    ap.add_argument("--out-dir", default="models", help="Output folder for ONNX + meta")
    args = ap.parse_args()

    models_dir = Path(args.models_dir)
    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    if not models_dir.is_dir():
        raise SystemExit(f"models-dir not found: {models_dir}")

    files = list(models_dir.rglob("*.pt")) + list(models_dir.rglob("*.pth"))
    if not files:
        raise SystemExit(f"no .pt/.pth under {models_dir}")

    assigned: Dict[str, Path] = {}
    for f in files:
        key = identify_model(f)
        if key and key not in assigned:
            assigned[key] = f

    print("=" * 60)
    print(f"models-dir: {models_dir}")
    print(f"out-dir:    {out_dir}")
    print(f"found:      {list(assigned.keys())}")
    print("=" * 60)

    meta_entries: Dict[str, dict] = {}
    ok = 0
    for key, cfg in MODEL_CONFIGS.items():
        if key not in assigned:
            print(f"\n[skip] {key} — no checkpoint")
            continue
        path = assigned[key]
        print(f"\n>> {key}  ({cfg['desc']})")
        print(f"   src: {path}")
        try:
            ckpt = load_checkpoint(path)
            meta = extract_meta(ckpt, cfg["names"], cfg["frames"])
            # prefer config names only if ckpt has none / wrong length
            if len(meta["names"]) != meta["n_classes"]:
                meta["names"] = cfg["names"][: meta["n_classes"]]
            print(
                f"   mean={meta['mean']:.4f}  std={meta['std']:.4f}  "
                f"frames={meta['patch_frames']}  arch={meta['arch']}"
            )
            if meta["mean"] == 0.0 and meta["std"] == 1.0:
                print("   [WARN] mean/std still 0/1 — checkpoint may lack normalization stats")

            model = build_and_load(ckpt, meta)
            onnx_path = out_dir / f"{key}.onnx"
            export_onnx(
                model,
                onnx_path,
                frames=meta["patch_frames"],
                arch=meta["arch"],
                phys_dim=meta.get("phys_dim", PHYS_DIM),
            )
            meta_entries[key] = meta
            ok += 1
        except Exception as e:
            print(f"   ❌ {e}")

    if meta_entries:
        write_meta(out_dir / "models_meta.json", meta_entries)

    print("\n" + "=" * 60)
    print(f"exported: {ok}")
    print("Next: copy ONNX + models_meta.json into Go models/, rebuild, run with --drums-only")
    print("Go must: hop=512, apply mean/std, for mel_phys feed input_phys (6-dim)")
    print("=" * 60)


if __name__ == "__main__":
    main()
