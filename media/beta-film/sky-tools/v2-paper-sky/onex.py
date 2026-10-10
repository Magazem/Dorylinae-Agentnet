"""Display-resolution copies of the v2 skies that the film moves or holds (dirfix, review r1 motion):
   python -I onex.py <in 2x.png> <out 1x.png>
The sky PNGs are made at 2x their display size. Drawn at half size, Chromium picks its downsampling filter (mip level)
from the sky's current scale, so as a following sky's scale changed the stars changed in single frames (a +12-level
pop at 16:9 frame 2836, twinkle through the IV push, breathing through the 4:5 wait). A copy at display size, downscaled
here with Lanczos, is drawn at scale >= 1 with no mip-mapping: no filter switch, no pop."""
import sys
from PIL import Image
src, dst = sys.argv[1:3]
im = Image.open(src).convert('RGB')
w, h = im.size
assert w % 2 == 0 and h % 2 == 0, (w, h)
im.resize((w // 2, h // 2), Image.LANCZOS).save(dst, compress_level=6)
print(dst.rsplit('/', 1)[-1], (w // 2, h // 2))
