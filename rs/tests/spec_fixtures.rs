// Every fixture in ../../test/spec has a runner in this directory; a new
// file added without one fails here rather than passing silently.

mod common;

use std::collections::BTreeSet;

use tabnas_support::{load_spec_dir, SpecOptions};

#[test]
fn every_fixture_has_a_runner() {
    let files: BTreeSet<String> = load_spec_dir(common::spec_dir(), &SpecOptions::default())
        .expect("the fixtures load")
        .into_iter()
        .map(|spec| spec.file)
        .collect();
    let runners: BTreeSet<String> = [
        ("csv.tsv", include_str!("spec_csv.rs")),
        ("json.tsv", include_str!("spec_json.rs")),
        ("number.tsv", include_str!("spec_number.rs")),
        ("records.tsv", include_str!("spec_records.rs")),
        ("text.tsv", include_str!("spec_text.rs")),
    ]
    .into_iter()
    .inspect(|(file, source)| {
        assert!(
            source.contains(&format!("common::spec(\"{file}\")")),
            "the runner for {file} reads it"
        )
    })
    .map(|(file, _)| file.to_string())
    .collect();
    assert_eq!(files, runners, "each fixture has a runner in rs/tests");
}
