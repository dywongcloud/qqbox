use std::collections::BTreeSet;

use crate::error::{Error, Result};
use crate::types::Arch;

const AMD64_SYSCALL: [u8; 2] = [0x0f, 0x05];
const ARM64_SVC0: [u8; 4] = [0x01, 0x00, 0x00, 0xd4];
const AMD64_UD2: [u8; 2] = [0x0f, 0x0b];
const ARM64_UDF0: [u8; 4] = [0x00, 0x00, 0x00, 0x00];
const AMD64_JMP_REL8: u8 = 0xeb;
const AMD64_JMP_REL32: u8 = 0xe9;
const ARM64_B: u32 = 0x1400_0000;
const ARM64_B_REACH: i128 = 1 << 27;
const REL8_MIN: i128 = -128;
const REL8_MAX: i128 = 127;
const I32_MIN: i128 = i32::MIN as i128;
const I32_MAX: i128 = i32::MAX as i128;
const AMD64_SITE_LEN: usize = 2;
const AMD64_NEAR_LEN: usize = 5;
const CAVE_LEN: usize = 5;

fn signature(arch: Arch) -> &'static [u8] {
    match arch {
        Arch::Amd64 => &AMD64_SYSCALL,
        Arch::Arm64 => &ARM64_SVC0,
    }
}

fn instruction_alignment(arch: Arch) -> usize {
    match arch {
        Arch::Amd64 => 1,
        Arch::Arm64 => 4,
    }
}

fn arch_name(arch: Arch) -> &'static str {
    match arch {
        Arch::Amd64 => "x86-64",
        Arch::Arm64 => "aarch64",
    }
}

fn is_site(code: &[u8], off: usize, signature: &[u8], alignment: usize) -> bool {
    if alignment == 0 || off % alignment != 0 {
        return false;
    }
    match off.checked_add(signature.len()) {
        Some(end) if end <= code.len() => &code[off..end] == signature,
        _ => false,
    }
}

/// Byte offsets of the syscall instructions in `code`.
///
/// x86-64 matches only the two-byte `0f 05` at any instruction boundary;
/// `0f 34` (sysenter) and every other pair are data. aarch64 matches the
/// boundaries, and ignores a trailing partial word. The aarch64 signature is
/// the little-endian byte order of the instruction word 0xd4000001.
pub fn classify(arch: Arch, code: &[u8]) -> Vec<usize> {
    let signature = signature(arch);
    let alignment = instruction_alignment(arch);
    let mut sites = Vec::new();
    if signature.is_empty() || alignment == 0 || code.len() < signature.len() {
        return sites;
    }
    for (index, window) in code.windows(signature.len()).step_by(alignment).enumerate() {
        if window == signature {
            if let Some(off) = index.checked_mul(alignment) {
                sites.push(off);
            }
        }
    }
    sites
}

/// Rewrites each validated syscall site into a PC-relative branch toward the
/// stub window, returning the number of distinct sites rewritten.
///
/// Offsets into `code` are guest virtual addresses with the image based at
/// zero, so `stub_window` is the stub entry's address in that space. An x86-64
/// site gets a two-byte `jmp rel8` when the window is in short range, else a
/// five-byte `jmp rel32` reached through a padding cave or written in place.
/// An aarch64 site gets a four-byte `b` whose imm26 stays inside B range.
/// A site that no branch can reach keeps an in-place trap (`ud2` / `udf #0`)
/// that the stub serves. Invalid, misaligned or out-of-bounds offsets are
/// rejected before any byte is written.
pub fn patch(arch: Arch, code: &mut [u8], sites: &[usize], stub_window: u64) -> Result<usize> {
    let signature = signature(arch);
    let alignment = instruction_alignment(arch);
    let mut unique = BTreeSet::new();
    for &off in sites {
        if !is_site(code, off, signature, alignment) {
            return Err(Error::Config(format!(
                "offset {} is not a {} syscall site",
                off,
                arch_name(arch)
            )));
        }
        unique.insert(off);
    }
    let mut written: Vec<(usize, usize)> = Vec::new();
    let mut rewritten = 0usize;
    for off in &unique {
        rewrite_site(arch, code, *off, stub_window, &unique, &mut written)?;
        rewritten += 1;
    }
    Ok(rewritten)
}

fn rewrite_site(
    arch: Arch,
    code: &mut [u8],
    off: usize,
    stub_window: u64,
    sites: &BTreeSet<usize>,
    written: &mut Vec<(usize, usize)>,
) -> Result<()> {
    let target = i128::from(stub_window);
    match arch {
        Arch::Amd64 => {
            let short_disp = target - (off as i128 + AMD64_SITE_LEN as i128);
            if (REL8_MIN..=REL8_MAX).contains(&short_disp) {
                let branch = [AMD64_JMP_REL8, short_disp as i8 as u8];
                write_bytes(code, off, &branch)?;
                written.push((off, off + AMD64_SITE_LEN));
                return Ok(());
            }
            let near_disp = target - (off as i128 + AMD64_NEAR_LEN as i128);
            if (I32_MIN..=I32_MAX).contains(&near_disp) {
                if let Some(cave) = find_cave(code, off, sites, written) {
                    let island_disp = target - (cave as i128 + CAVE_LEN as i128);
                    if (I32_MIN..=I32_MAX).contains(&island_disp) {
                        let mut island = [AMD64_JMP_REL32, 0, 0, 0, 0];
                        island[1..].copy_from_slice(&(island_disp as i32).to_le_bytes());
                        write_bytes(code, cave, &island)?;
                        let cave_disp = cave as i128 - (off as i128 + AMD64_SITE_LEN as i128);
                        let branch = [AMD64_JMP_REL8, cave_disp as i8 as u8];
                        write_bytes(code, off, &branch)?;
                        written.push((cave, cave + CAVE_LEN));
                        written.push((off, off + AMD64_SITE_LEN));
                        return Ok(());
                    }
                }
                if let Some(end) = off.checked_add(AMD64_NEAR_LEN) {
                    let clear_of_sites = !sites
                        .iter()
                        .any(|&site| site != off && overlaps(off, end, site, site + AMD64_SITE_LEN));
                    let clear_of_writes = !written
                        .iter()
                        .any(|&(start, stop)| overlaps(off, end, start, stop));
                    if end <= code.len() && clear_of_sites && clear_of_writes {
                        let mut branch = [AMD64_JMP_REL32, 0, 0, 0, 0];
                        branch[1..].copy_from_slice(&(near_disp as i32).to_le_bytes());
                        write_bytes(code, off, &branch)?;
                        written.push((off, end));
                        return Ok(());
                    }
                }
            }
            write_bytes(code, off, &AMD64_UD2)?;
            written.push((off, off + AMD64_SITE_LEN));
            Ok(())
        }
        Arch::Arm64 => {
            let disp = target - off as i128;
            if disp % 4 == 0 && disp >= -ARM64_B_REACH && disp < ARM64_B_REACH {
                let imm = ((disp / 4) as i32 as u32) & 0x03ff_ffff;
                write_bytes(code, off, &(ARM64_B | imm).to_le_bytes())?;
            } else {
                write_bytes(code, off, &ARM64_UDF0)?;
            }
            written.push((off, off + ARM64_SVC0.len()));
            Ok(())
        }
    }
}

fn is_padding(byte: u8) -> bool {
    matches!(byte, 0x90 | 0xcc | 0x00)
}

fn overlaps(a_start: usize, a_end: usize, b_start: usize, b_end: usize) -> bool {
    a_start < b_end && b_start < a_end
}

fn find_cave(
    code: &[u8],
    off: usize,
    sites: &BTreeSet<usize>,
    written: &[(usize, usize)],
) -> Option<usize> {
    if code.len() < CAVE_LEN {
        return None;
    }
    let after = off as i128 + AMD64_SITE_LEN as i128;
    let low = (after + REL8_MIN).max(0);
    let high = (after + REL8_MAX).min(code.len() as i128 - CAVE_LEN as i128);
    for candidate in low..=high {
        let cave = candidate as usize;
        let end = match cave.checked_add(CAVE_LEN) {
            Some(end) if end <= code.len() => end,
            _ => continue,
        };
        if !code[cave..end].iter().all(|byte| is_padding(*byte)) {
            continue;
        }
        if sites
            .iter()
            .any(|&site| overlaps(cave, end, site, site + AMD64_SITE_LEN))
        {
            continue;
        }
        if written
            .iter()
            .any(|&(start, stop)| overlaps(cave, end, start, stop))
        {
            continue;
        }
        return Some(cave);
    }
    None
}

fn write_bytes(code: &mut [u8], off: usize, bytes: &[u8]) -> Result<()> {
    match off.checked_add(bytes.len()) {
        Some(end) if end <= code.len() => {
            code[off..end].copy_from_slice(bytes);
            Ok(())
        }
        _ => Err(Error::Config(format!(
            "rewrite at offset {} needs {} bytes beyond the code",
            off,
            bytes.len()
        ))),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn amd64_only_syscall_is_a_site() {
        let code = [0x0f, 0x34, 0x90, 0x0f, 0x05, 0x0f, 0x05];
        assert_eq!(classify(Arch::Amd64, &code), vec![3, 5]);
    }

    #[test]
    fn arm64_sites_are_word_aligned() {
        let code = [
            0x90, 0xd4, 0x00, 0x00, 0x01, 0x90, 0x90, 0x90, 0xd4, 0x00, 0x00, 0x01,
        ];
        assert_eq!(classify(Arch::Arm64, &code), vec![8]);
    }

    #[test]
    fn truncated_words_are_ignored() {
        assert!(classify(Arch::Amd64, &[0x0f]).is_empty());
        assert!(classify(Arch::Arm64, &[0xd4, 0x00, 0x00]).is_empty());
    }

    #[test]
    fn patch_rejects_unchecked_offsets_without_writing() {
        let mut code = [0x0f, 0x05];
        assert!(patch(Arch::Amd64, &mut code, &[usize::MAX], 0x1000).is_err());
        assert_eq!(code, [0x0f, 0x05]);
    }

    #[test]
    fn patch_emits_short_branch() {
        let mut code = [0x90; 16];
        code[4] = 0x0f;
        code[5] = 0x05;
        let sites = classify(Arch::Amd64, &code);
        assert_eq!(sites, vec![4]);
        let count = patch(Arch::Amd64, &mut code, &sites, 32).unwrap_or(0);
        assert_eq!(count, 1);
        assert_eq!(code[4], AMD64_JMP_REL8);
        assert_eq!(code[5] as i8, 26);
    }

    #[test]
    fn patch_emits_arm64_branch() {
        let mut code = [
            0xd5, 0x03, 0x20, 0x1f, 0xd4, 0x00, 0x00, 0x01, 0xd6, 0x5f, 0x03, 0xc0,
        ];
        let sites = classify(Arch::Arm64, &code);
        assert_eq!(sites, vec![4]);
        let count = patch(Arch::Arm64, &mut code, &sites, 0x1000).unwrap_or(0);
        assert_eq!(count, 1);
        let word = u32::from_le_bytes([code[4], code[5], code[6], code[7]]);
        assert_eq!(word & 0xfc00_0000, ARM64_B);
    }
}
