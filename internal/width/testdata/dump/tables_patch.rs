
// ---------------------------------------------------------------------------
// Appended by the Coigue width dump harness (internal/width/testdata/dump).
// Exposes the private pieces of unicode-width 0.2.2 so they can be dumped.
// ---------------------------------------------------------------------------

/// The raw 2-bit entry of the WIDTH_ROOT/MIDDLE/LEAVES trie (3 = "special, see lookup_width").
pub fn dbg_raw(cp: u32) -> u8 {
    let cp = cp as usize;
    let t1_offset = WIDTH_ROOT.0[cp >> 13];
    let t2_offset = WIDTH_MIDDLE.0[usize::from(t1_offset)][cp >> 7 & 0x3F];
    let packed_widths = WIDTH_LEAVES.0[usize::from(t2_offset)][cp >> 2 & 0x1F];
    packed_widths >> (2 * (cp & 0b11)) & 0b11
}

/// lookup_width with the WidthInfo flattened to its u16 representation.
pub fn dbg_lookup(c: char) -> (u8, u16) {
    let (w, info) = lookup_width(c);
    (w, info.0)
}

pub fn dbg_transparent_zero_width(c: char) -> bool {
    is_transparent_zero_width(c)
}

pub fn dbg_ligature_transparent(c: char) -> bool {
    is_ligature_transparent(c)
}

/// NON_TRANSPARENT_ZERO_WIDTHS decoded to inclusive code point ranges.
pub fn dbg_non_transparent_zero_widths() -> Vec<(u32, u32)> {
    NON_TRANSPARENT_ZERO_WIDTHS
        .iter()
        .map(|&(lo, hi)| {
            (
                u32::from_le_bytes([lo[0], lo[1], lo[2], 0]),
                u32::from_le_bytes([hi[0], hi[1], hi[2], 0]),
            )
        })
        .collect()
}

/// One step of the reverse fold: width_in_str with WidthInfo as u16.
pub fn dbg_width_in_str(c: char, info: u16) -> (i8, u16) {
    let (add, next) = width_in_str(c, WidthInfo(info));
    (add, next.0)
}
