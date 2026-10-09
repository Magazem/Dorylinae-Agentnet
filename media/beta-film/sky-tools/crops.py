"""Before / after crops at 3x (nearest neighbour), before above after. python crops.py <spec.json> <outdir>
spec: [[name, before.png, after.png, [x0, y0, x1, y1]], ...]"""
import sys, json, os
from PIL import Image, ImageDraw
spec, out = json.load(open(sys.argv[1])), sys.argv[2]
os.makedirs(out, exist_ok=True)
for name, b, a, box in spec:
    tiles = []
    for lab, p in (('before', b), ('after', a)):
        im = Image.open(p).convert('RGB').crop(tuple(box))
        im = im.resize((im.width * 3, im.height * 3), Image.NEAREST)
        d = ImageDraw.Draw(im); d.rectangle([0, 0, 70, 18], fill=(0, 0, 0)); d.text((4, 3), lab, fill=(255, 255, 0))
        tiles.append(im)
    w = tiles[0].width; h = tiles[0].height
    sheet = Image.new('RGB', (w, 2 * h + 6), (255, 255, 0))
    sheet.paste(tiles[0], (0, 0)); sheet.paste(tiles[1], (0, h + 6))
    sheet.save(f'{out}/{name}.png')
    print(f'{out}/{name}.png')
