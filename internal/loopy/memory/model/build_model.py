#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Builds vectors.bin, the word vectors Loopy's memory searches by meaning
with, from minishlab/potion-multilingual-128M (MIT, 512 MB): its Latin
tokens, the most frequent first, brought to 128 dimensions in int8.

  build_model.py POTION_DIR OUT TOKENS DIMENSIONS
(needs numpy and safetensors). What each size costs in recall is in
docs/LOOPY.md.
"""
import json, re, struct, sys
import numpy as np
from safetensors.numpy import load_file

source, out, count, dim = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
vectors = load_file(source + "/model.safetensors")
vectors = vectors[list(vectors)[0]].astype(np.float32)
tokens = json.load(open(source + "/tokenizer.json"))["model"]["vocab"]
latin = re.compile("[▁\\x21-\\x7e -ɏ‐-›€]+")
score = np.array([t[1] for t in tokens])
order = [i for i in np.argsort(-score, kind="stable") if i > 1 and latin.fullmatch(tokens[i][0])][:count]
sample = vectors[np.random.RandomState(0).choice(len(vectors), 40000, replace=False)]
_, _, axes = np.linalg.svd(sample, full_matrices=False)
small = vectors[order] @ axes[:dim].T
scale = np.abs(small).max(1) + 1e-9
packed = np.round(small / scale[:, None] * 127).astype(np.int8)
with open(out, "wb") as f:
    f.write(b"LPV1" + struct.pack("<IH", len(order), dim))
    for i in order:
        name = tokens[i][0].encode()
        f.write(struct.pack("<Bf", len(name), tokens[i][1]) + name)
    f.write(scale.astype("<f4").tobytes())
    f.write(packed.tobytes())
