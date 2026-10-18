# -*- coding: utf-8 -*-
"""Dense frame sampling for video UI study.

Usage: py -3.14 -I extract_frames.py <video> <out_dir> <interval_s> <crop_y0> <crop_y1>
Writes per-frame JPGs plus contact sheets of 8 tiles each.
"""
import sys
from pathlib import Path

import cv2


def write_jpg(path: Path, img, quality: int = 88) -> bool:
    """Unicode-safe JPEG write (cv2.imwrite fails silently on CJK paths)."""
    ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, quality])
    if not ok:
        return False
    path.write_bytes(buf.tobytes())
    return True


def main() -> int:
    video, out_dir = sys.argv[1], Path(sys.argv[2])
    interval = float(sys.argv[3])
    y0, y1 = int(sys.argv[4]), int(sys.argv[5])
    prefix = sys.argv[6] if len(sys.argv) > 6 else "f"

    out_dir.mkdir(parents=True, exist_ok=True)
    cap = cv2.VideoCapture(video)
    if not cap.isOpened():
        print("cannot open", video)
        return 1
    fps = cap.get(cv2.CAP_PROP_FPS) or 30.0
    total = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))
    dur = total / fps
    print(f"fps={fps:.2f} frames={total} dur={dur:.2f}s")

    tiles, sheet_idx, saved = [], 0, 0
    t = 0.0
    while t <= dur:
        cap.set(cv2.CAP_PROP_POS_MSEC, t * 1000.0)
        ok, frame = cap.read()
        if not ok:
            break
        crop = frame[y0:y1, 0 : frame.shape[1]]
        name = f"{prefix}_{t:06.2f}s.jpg"
        if write_jpg(out_dir / name, crop, 88):
            saved += 1
        small = cv2.resize(crop, (crop.shape[1] // 3, crop.shape[0] // 3))
        # label bar
        lab = cv2.copyMakeBorder(small, 26, 0, 0, 0, cv2.BORDER_CONSTANT, value=(30, 34, 40))
        cv2.putText(lab, f"{t:06.2f}s", (8, 19), cv2.FONT_HERSHEY_SIMPLEX, 0.55, (240, 240, 240), 1, cv2.LINE_AA)
        tiles.append(lab)
        if len(tiles) == 9:
            sheet_idx += 1
            rows = [cv2.hconcat(tiles[i : i + 3]) for i in range(0, 9, 3)]
            write_jpg(out_dir / f"sheet_{sheet_idx:02d}.jpg", cv2.vconcat(rows), 85)
            tiles = []
        t += interval
    if tiles:
        while len(tiles) % 3:
            tiles.append(tiles[-1] * 0)
        sheet_idx += 1
        rows = [cv2.hconcat(tiles[i : i + 3]) for i in range(0, len(tiles), 3)]
        write_jpg(out_dir / f"sheet_{sheet_idx:02d}.jpg", cv2.vconcat(rows), 85)
    cap.release()
    print(f"saved {saved} frames, {sheet_idx} sheets -> {out_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
