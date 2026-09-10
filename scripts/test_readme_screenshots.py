"""Small, offline tests for README screenshot integrity checks."""
import importlib.util
import struct
import unittest
import zlib
from pathlib import Path

SPEC = importlib.util.spec_from_file_location('readme_images', Path(__file__).with_name('readme-screenshots.py'))
images = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(images)


def chunk(kind, content=b''):
    return struct.pack('>I', len(content)) + kind + content + struct.pack('>I', zlib.crc32(kind + content) & 0xffffffff)


def png(width=2, height=2, color=6, payload=None):
    header = chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, color, 0, 0, 0))
    channels = 3 if color == 2 else 4
    raw = (b'\x00' + b'\x80' * (2 * channels)) * 2
    return b'\x89PNG\r\n\x1a\n' + header + chunk(b'IDAT', zlib.compress(raw) if payload is None else payload) + chunk(b'IEND')


class PngIntegrityTests(unittest.TestCase):
    def test_valid_rgb(self):
        self.assertEqual(images.png_info(png(color=2)), (2, 2))

    def test_valid_rgba(self):
        self.assertEqual(images.png_info(png()), (2, 2))

    def test_invalid_crc(self):
        data = bytearray(png())
        data[32] ^= 1
        with self.assertRaisesRegex(ValueError, 'CRC'):
            images.png_info(bytes(data))

    def test_truncated_file(self):
        with self.assertRaises(ValueError):
            images.png_info(png()[:-5])

    def test_trailing_bytes(self):
        with self.assertRaisesRegex(ValueError, 'ending'):
            images.png_info(png() + b'not part of the image')

    def test_excessive_dimensions(self):
        with self.assertRaisesRegex(ValueError, 'dimensions'):
            images.png_info(png(width=2049))

    def test_incomplete_pixels_with_valid_crc(self):
        with self.assertRaisesRegex(ValueError, 'pixel payload'):
            images.png_info(png(payload=zlib.compress(b'\x00')))


if __name__ == '__main__':
    unittest.main()
