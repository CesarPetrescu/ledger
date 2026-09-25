#!/usr/bin/env python3
"""Import approved CI screenshots, or verify the committed README image set.

Only three named PNGs are copied. Logs, packages, storage and credentials are
never imported. Images are kept byte-for-byte; there is no fabricated UI.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import struct
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEST = ROOT / 'assets' / 'screenshots'
FILES = {'website.png', 'android.png', 'even-simulator.png'}


def png_info(data: bytes) -> tuple[int, int]:
    """Check chunk CRCs and exact decompressed dimensions, with bounded memory."""
    if not 45 <= len(data) <= 2_000_000 or data[:8] != b'\x89PNG\r\n\x1a\n':
        raise ValueError('Expected a bounded PNG file')
    pos, dimensions, compressed, done = 8, None, bytearray(), False
    while pos < len(data):
        if pos + 12 > len(data):
            raise ValueError('Truncated PNG chunk')
        size, kind = struct.unpack('>I4s', data[pos:pos + 8])
        end = pos + 12 + size
        if end > len(data):
            raise ValueError('Truncated PNG data')
        content = data[pos + 8:pos + 8 + size]
        crc = struct.unpack('>I', data[pos + 8 + size:end])[0]
        if zlib.crc32(kind + content) & 0xffffffff != crc:
            raise ValueError('PNG CRC mismatch')
        if pos == 8 and kind != b'IHDR':
            raise ValueError('Missing initial PNG header')
        if kind == b'IHDR':
            if dimensions is not None or size != 13:
                raise ValueError('Invalid PNG header')
            width, height, depth, color, compression, filtering, interlace = struct.unpack('>IIBBBBB', content)
            if not (0 < width <= 2048 and 0 < height <= 4096):
                raise ValueError('Unexpected screenshot dimensions')
            if depth != 8 or color not in (2, 6) or compression or filtering or interlace:
                raise ValueError('Expected non-interlaced RGB/RGBA 8-bit screenshot')
            dimensions = (width, height, 3 if color == 2 else 4)
        elif kind == b'IDAT':
            compressed.extend(content)
        elif kind == b'IEND':
            if size != 0 or end != len(data):
                raise ValueError('Invalid PNG ending')
            done = True
            break
        pos = end
    if not done or not dimensions or not compressed:
        raise ValueError('Incomplete screenshot')
    width, height, channels = dimensions
    expected = height * (1 + width * channels)
    decoder = zlib.decompressobj()
    raw = decoder.decompress(compressed, expected + 1)
    if len(raw) != expected or not decoder.eof or decoder.unused_data or decoder.unconsumed_tail:
        raise ValueError('PNG pixel payload does not match dimensions')
    return width, height


def find_one(directory: Path, suffix: str) -> Path:
    root = directory.resolve(strict=True)
    found = [p for p in root.rglob(Path(suffix).name) if p.as_posix().endswith(suffix)]
    if len(found) != 1:
        raise ValueError(f'Expected exactly one {suffix}, found {len(found)}')
    path = found[0]
    if path.is_symlink() or not path.resolve().is_relative_to(root):
        raise ValueError('Artifact image escapes its input directory')
    return path


def import_images(args: argparse.Namespace) -> None:
    if not re.fullmatch(r'[0-9a-f]{40}', args.head_sha) or args.run_id <= 0:
        raise ValueError('Use a concrete CI run and full commit SHA')
    mapping = [
        ('website.png', args.native, 'system/artifacts/readme-website.png', 'glass-system-native-web', 'Chromium: real web UI and Ledger backend; fictional project records'),
        ('android.png', args.android, 'android/app/build/reports/readme/android-overview.png', 'android-reports-api-36', 'Android API 36 emulator: real app; existing HTTPS fixture backend'),
        ('even-simulator.png', args.native, 'system/artifacts/preview-03-now-real-data.png', 'glass-system-native-web', 'Official Even Hub simulator: real app/backend; framebuffer composited onto black'),
    ]
    images, entries = {}, []
    for name, directory, suffix, artifact, description in mapping:
        path = find_one(directory, suffix)
        data = path.read_bytes()
        width, height = png_info(data)
        if width < 320 or height < 200:
            raise ValueError(f'{name}: screenshot is too small')
        if name == 'android.png' and height <= width:
            raise ValueError('Android screenshot must be portrait')
        if name == 'even-simulator.png' and (width, height) != (576, 288):
            raise ValueError('Unexpected native glasses framebuffer dimensions')
        images[name] = data
        entries.append({'file': name, 'sha256': hashlib.sha256(data).hexdigest(),
                        'width': width, 'height': height, 'bytes': len(data),
                        'artifact': artifact, 'source_path_suffix': suffix,
                        'description': description})
    source_commit = find_one(args.native, 'system/artifacts/tested-commit.txt').read_text().strip()
    if not re.fullmatch(r'[0-9a-f]{40}', source_commit):
        raise ValueError('Missing tested revision in native artifact')
    provenance = {'version': 1, 'repository': 'CesarPetrescu/ledger',
                  'capture_run_id': args.run_id,
                  'capture_run_url': f'https://github.com/CesarPetrescu/ledger/actions/runs/{args.run_id}',
                  'pr_head_sha': args.head_sha, 'tested_commit': source_commit,
                  'fixture_data': True, 'images': entries}
    DEST.mkdir(parents=True, exist_ok=True)
    for name, data in images.items():
        (DEST / name).write_bytes(data)
    (DEST / 'provenance.json').write_text(json.dumps(provenance, indent=2) + '\n')
    print('Imported three validated, unchanged screenshot assets')


def check_images() -> None:
    provenance = json.loads((DEST / 'provenance.json').read_text())
    entries = provenance.get('images', [])
    if len(entries) != 3 or {e['file'] for e in entries} != FILES:
        raise ValueError('README requires exactly the three documented surfaces')
    readme = (ROOT / 'README.md').read_text()
    for entry in entries:
        name = entry['file']
        data = (DEST / name).read_bytes()
        width, height = png_info(data)
        if (width, height, len(data)) != (entry['width'], entry['height'], entry['bytes']):
            raise ValueError(f'{name}: metadata mismatch')
        if hashlib.sha256(data).hexdigest() != entry['sha256']:
            raise ValueError(f'{name}: checksum mismatch')
        pattern = rf'<img\b[^>]*src="assets/screenshots/{re.escape(name)}"[^>]*alt="[^"]+"'
        if not re.search(pattern, readme):
            raise ValueError(f'{name}: missing relative README image/alt text')
        print(f'PASS {name}: {width}x{height}, {len(data)} bytes, checksum and README link')
    if 'intentionally read-only' in readme:
        raise ValueError('Obsolete Ledger Glass feature description')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('check', help='Validate committed screenshots and README references')
    imp = sub.add_parser('import', help='Copy three screenshots from inspected CI artifact directories')
    imp.add_argument('--native', type=Path, required=True)
    imp.add_argument('--android', type=Path, required=True)
    imp.add_argument('--run-id', type=int, required=True)
    imp.add_argument('--head-sha', required=True)
    args = parser.parse_args()
    if args.command == 'import':
        import_images(args)
    else:
        check_images()
