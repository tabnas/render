//! Number text, shared by the renderers.
//!
//! A number reaches a renderer as a machine value and, when the source
//! could hand it over, the lexeme it was spelled with. The lexeme wins,
//! because it is the only thing that keeps `50.25` as `50.25` and keeps the
//! digits of a number beyond f64's exact range; but a lexeme is data from a
//! source, so it is checked against the JSON number grammar before it is
//! copied into an output that promises to be JSON or CSV. Without a lexeme
//! the shortest text that reads back as the same f64 is written. A value
//! with no finite text (NaN, infinity) is rejected as unrepresentable
//! rather than written as `null`, which would silently change the data, and
//! it is rejected whatever lexeme stands beside it: `1e999` spells a
//! number, but the value the pipeline holds is infinity, and a JSON reader
//! given the lexeme refuses it as out of range.
//!
//! Validation and formatting are separate functions so a renderer can check
//! a whole row before it writes any of it, and format each number once.

use std::fmt::Write as _;

use tabnas_alchemy::shared::{Code, Fail};

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

/// Whether a renderer may write this number at all.
///
/// A lexeme that is not a JSON number is `INVALID_NUMBER`. A value that is
/// not finite is `TARGET_VALUE_UNREPRESENTABLE`, with or without a lexeme:
/// the design brief names NaN and infinity unrepresentable, and a lexeme
/// such as `1e999` would hand the reader a number the pipeline never had
/// (serde_json rejects it as out of range). Nothing is formatted here, so a
/// renderer can run this over a whole row before writing a byte of it.
pub(crate) fn check_number(value: f64, lexeme: Option<&str>) -> Result<(), Fail> {
    if let Some(l) = lexeme {
        if !is_json_number(l) {
            return Err(Fail::new(
                Code::InvalidNumber,
                format!("{l:?} is not a JSON number"),
            ));
        }
    }
    if !value.is_finite() {
        let message = match lexeme {
            Some(l) => format!("{l:?} is {value} as a number, which has no representation"),
            None => format!("{value} has no representation as a number"),
        };
        return Err(Fail::new(Code::TargetValueUnrepresentable, message));
    }
    Ok(())
}

/// The magnitudes written positionally: from `1e-6` up to, not including,
/// `1e21`. These are the thresholds JavaScript's `Number#toString` uses,
/// so they are the ones most JSON in circulation was written with; an
/// integer of up to 21 digits stays an integer, and `1e300` is five
/// characters rather than 301.
const POSITIONAL_MIN: f64 = 1e-6;
const POSITIONAL_MAX: f64 = 1e21;

/// Write the shortest text that reads back as `value`, into `out`
/// (cleared first), and hand it back as a slice of `out`.
///
/// Rust's float formatting produces the shortest digit string that
/// round-trips; this function only chooses the layout. Positional form for
/// zero and for magnitudes within [`POSITIONAL_MIN`, `POSITIONAL_MAX`),
/// exponent form (`1.5e300`, `-2.5e-8`) outside, because Rust's positional
/// form never uses an exponent and would spell `1e300` with 301 digits.
/// Both layouts are JSON numbers. The value must be finite, which
/// [`check_number`] establishes before every call: a non-finite value has
/// no JSON text and is refused there, not here.
pub(crate) fn write_value(value: f64, out: &mut String) -> &str {
    out.clear();
    let magnitude = value.abs();
    // Writing into a String cannot fail; the Result is fmt's, not a writer's.
    let _ = if magnitude == 0.0 || (POSITIONAL_MIN..POSITIONAL_MAX).contains(&magnitude) {
        write!(out, "{value}")
    } else {
        write!(out, "{value:e}")
    };
    out
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
    fn a_json_number_lexeme_beside_a_finite_value_passes() {
        assert_eq!(check_number(1.0, Some("1.00")), Ok(()));
        assert_eq!(check_number(1.0, None), Ok(()));
        assert_eq!(
            check_number(1e30, Some("123456789012345678901234567890")),
            Ok(())
        );
    }

    #[test]
    fn a_lexeme_that_is_not_a_json_number_is_invalid_number() {
        assert_eq!(
            check_number(1.0, Some("1.")).unwrap_err().code,
            Code::InvalidNumber
        );
        // The lexeme is judged first: a NaN spelled "NaN" is a bad lexeme,
        // not an unrepresentable value.
        assert_eq!(
            check_number(f64::NAN, Some("NaN")).unwrap_err().code,
            Code::InvalidNumber
        );
    }

    #[test]
    fn a_non_finite_value_is_unrepresentable_with_or_without_a_lexeme() {
        for v in [f64::NAN, f64::INFINITY, f64::NEG_INFINITY] {
            assert_eq!(
                check_number(v, None).unwrap_err().code,
                Code::TargetValueUnrepresentable
            );
        }
        let err = check_number(f64::INFINITY, Some("1e999")).unwrap_err();
        assert_eq!(err.code, Code::TargetValueUnrepresentable);
        assert!(err.message.contains("\"1e999\""), "{}", err.message);
        assert_eq!(
            check_number(f64::NEG_INFINITY, Some("-1e999"))
                .unwrap_err()
                .code,
            Code::TargetValueUnrepresentable
        );
    }

    fn text(value: f64) -> String {
        let mut scratch = String::new();
        write_value(value, &mut scratch).to_owned()
    }

    #[test]
    fn a_value_takes_the_shortest_form_positional_within_the_javascript_range() {
        assert_eq!(text(1.0), "1");
        assert_eq!(text(0.1), "0.1");
        assert_eq!(text(-0.0), "-0");
        assert_eq!(text(0.0), "0");
        assert_eq!(text(50.25), "50.25");
        assert_eq!(text(123456.789), "123456.789");
        assert_eq!(text(1e20), "100000000000000000000");
        assert_eq!(text(1.5e17), "150000000000000000");
        assert_eq!(text(1.23456789e18), "1234567890000000000");
        assert_eq!(text(0.000001), "0.000001");
        assert_eq!(text(-0.000025), "-0.000025");
    }

    #[test]
    fn a_value_outside_the_positional_range_takes_the_exponent_form() {
        assert_eq!(text(1e21), "1e21");
        assert_eq!(text(1e300), "1e300");
        assert_eq!(text(1e-300), "1e-300");
        assert_eq!(text(1e-7), "1e-7");
        assert_eq!(text(-2.5e-8), "-2.5e-8");
        assert_eq!(text(5e-324), "5e-324");
        assert_eq!(text(f64::MAX), "1.7976931348623157e308");
        assert_eq!(text(-1.5e300), "-1.5e300");
    }

    #[test]
    fn every_form_is_a_json_number_that_reads_back_as_the_same_value() {
        let values = [
            0.0,
            -0.0,
            1.0,
            0.1,
            1e20,
            1e21,
            1e300,
            1e-300,
            1e-6,
            1e-7,
            5e-324,
            f64::MAX,
            f64::MIN,
            f64::MIN_POSITIVE,
            123456.789,
            2f64.powi(53),
            9.999999999999999e20,
        ];
        let mut scratch = String::new();
        for v in values {
            let t = write_value(v, &mut scratch);
            assert!(is_json_number(t), "{v:e} wrote {t:?}");
            let back: f64 = t.parse().unwrap_or(f64::NAN);
            assert_eq!(back.to_bits(), v.to_bits(), "{v:e} wrote {t:?}");
        }
    }

    #[test]
    fn the_reviewers_probe_is_bytes_not_hundreds_of_digits() {
        let total: usize = [1e300, 1e-300, 1.5e17, 1.23456789e18]
            .iter()
            .map(|&v| text(v).len())
            .sum();
        assert_eq!(total, 5 + 6 + 18 + 19);
    }
}
