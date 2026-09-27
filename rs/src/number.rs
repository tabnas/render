//! Number text, shared by the renderers.
//!
//! A number reaches a renderer as a machine value and, when the source
//! could hand it over, the lexeme it was spelled with. The lexeme wins,
//! because it is the only thing that keeps `50.25` as `50.25` and keeps the
//! digits of a number beyond f64's exact range; but a lexeme is data from a
//! source, so it is checked against the JSON number grammar before it is
//! copied into an output that promises to be JSON or CSV. Without a lexeme
//! the shortest round-trip text of the value is written, and a value with
//! no finite text (NaN, infinity) is rejected as unrepresentable rather
//! than written as `null`, which would silently change the data.

use tabnas_transduce::{write_json_number, Code, Fail};

/// Whether `text` is a number by RFC 8259's grammar:
/// `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`, nothing else and
/// nothing around it.
pub fn is_json_number(text: &str) -> bool {
    let b = text.as_bytes();
    let mut i = 0;
    if b.first() == Some(&b'-') {
        i += 1;
    }
    match b.get(i) {
        Some(b'0') => i += 1,
        Some(b'1'..=b'9') => {
            i += 1;
            while matches!(b.get(i), Some(b'0'..=b'9')) {
                i += 1;
            }
        }
        _ => return false,
    }
    if b.get(i) == Some(&b'.') {
        i += 1;
        let start = i;
        while matches!(b.get(i), Some(b'0'..=b'9')) {
            i += 1;
        }
        if i == start {
            return false;
        }
    }
    if matches!(b.get(i), Some(b'e' | b'E')) {
        i += 1;
        if matches!(b.get(i), Some(b'+' | b'-')) {
            i += 1;
        }
        let start = i;
        while matches!(b.get(i), Some(b'0'..=b'9')) {
            i += 1;
        }
        if i == start {
            return false;
        }
    }
    i == b.len()
}

/// The text a renderer writes for a number: the validated lexeme, or the
/// shortest round-trip form of the value built in `scratch`.
///
/// A lexeme that is not a JSON number is `INVALID_NUMBER`; a non-finite
/// value with no lexeme is `TARGET_VALUE_UNREPRESENTABLE`. A lexeme is
/// written even when the value beside it overflowed to infinity (`1e999`):
/// the source spelled a number, and what a reader makes of its range is the
/// reader's business.
pub(crate) fn number_text<'a>(
    value: f64,
    lexeme: Option<&'a str>,
    scratch: &'a mut String,
) -> Result<&'a str, Fail> {
    match lexeme {
        Some(l) if is_json_number(l) => Ok(l),
        Some(l) => Err(Fail::new(
            Code::InvalidNumber,
            format!("{l:?} is not a JSON number"),
        )),
        None if value.is_finite() => {
            scratch.clear();
            write_json_number(value, None, scratch);
            Ok(scratch.as_str())
        }
        None => Err(Fail::new(
            Code::TargetValueUnrepresentable,
            format!("{value} has no representation as a number"),
        )),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_json_number_grammar_is_exact() {
        for ok in [
            "0",
            "-0",
            "1",
            "-1",
            "10",
            "1.5",
            "0.0",
            "1e5",
            "1E5",
            "1e+5",
            "1e-5",
            "1.5e10",
            "123456789012345678901234567890",
            "-0.000001",
        ] {
            assert!(is_json_number(ok), "{ok:?} should be a number");
        }
        for bad in [
            "",
            "-",
            "+1",
            "01",
            "1.",
            ".5",
            "1e",
            "1e+",
            "1.e5",
            "0x10",
            "NaN",
            "Infinity",
            "-Infinity",
            " 1",
            "1 ",
            "1_000",
            "1,5",
            "١",
        ] {
            assert!(!is_json_number(bad), "{bad:?} should not be a number");
        }
    }

    #[test]
    fn a_lexeme_wins_and_a_value_falls_back_to_the_shortest_form() {
        let mut scratch = String::new();
        assert_eq!(
            number_text(1.0, Some("1.00"), &mut scratch).unwrap(),
            "1.00"
        );
        assert_eq!(number_text(1.0, None, &mut scratch).unwrap(), "1");
        assert_eq!(number_text(0.1, None, &mut scratch).unwrap(), "0.1");
        assert_eq!(number_text(-0.0, None, &mut scratch).unwrap(), "-0");
        assert_eq!(
            number_text(1e21, None, &mut scratch).unwrap(),
            "1000000000000000000000"
        );
        assert_eq!(
            number_text(f64::INFINITY, Some("1e999"), &mut scratch).unwrap(),
            "1e999"
        );
    }

    #[test]
    fn bad_lexemes_and_non_finite_values_are_rejected() {
        let mut scratch = String::new();
        assert_eq!(
            number_text(1.0, Some("1."), &mut scratch).unwrap_err().code,
            Code::InvalidNumber
        );
        for v in [f64::NAN, f64::INFINITY, f64::NEG_INFINITY] {
            assert_eq!(
                number_text(v, None, &mut scratch).unwrap_err().code,
                Code::TargetValueUnrepresentable
            );
        }
    }
}
