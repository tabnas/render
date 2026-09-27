//! The rendered JSON, read back by an independent reader.
//!
//! Every fixture a grammar in the dev-dependencies reads (copied from
//! aless's `tests/fixtures/`) is parsed, walked with transduce's
//! `ValueSource` into the renderer, and the text is parsed again by
//! serde_json. The result must equal the engine value's own `to_json()`,
//! member order included: the comparison is on the serialized form, since
//! serde_json compares maps as sets. Numbers are compared as f64 on both
//! sides, because the walker hands the renderer values without lexemes and
//! `to_json` builds floats, so `3` and `3.0` are the same number here.

use serde_json::Value as Json;
use tabnas_render::{JsonOptions, JsonRenderer, StringOut};
use tabnas_transduce::{Source, ValueSource};

fn fixture(name: &str) -> String {
    let path = format!("{}/tests/fixtures/{name}", env!("CARGO_MANIFEST_DIR"));
    std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("{path}: {e}"))
}

/// Every number as the f64 serde_json parses it, so `3` and `3.0` agree.
fn normalize(v: Json) -> Json {
    match v {
        Json::Number(n) => n
            .as_f64()
            .and_then(serde_json::Number::from_f64)
            .map_or(Json::Null, Json::Number),
        Json::Array(items) => Json::Array(items.into_iter().map(normalize).collect()),
        Json::Object(members) => Json::Object(
            members
                .into_iter()
                .map(|(k, v)| (k, normalize(v)))
                .collect(),
        ),
        other => other,
    }
}

fn render(value: &tabnas::Value, options: JsonOptions) -> String {
    let mut renderer = JsonRenderer::new(StringOut::new(), options);
    ValueSource(value).run(&mut renderer).unwrap();
    assert!(renderer.is_done());
    renderer.into_inner().into_string()
}

fn assert_round_trip(name: &str, value: &tabnas::Value) {
    let want = normalize(value.to_json()).to_string();
    for options in [
        JsonOptions::default(),
        JsonOptions {
            indent: Some(2),
            trailing_newline: true,
        },
    ] {
        let text = render(value, options.clone());
        let parsed: Json = serde_json::from_str(&text)
            .unwrap_or_else(|e| panic!("{name} with {options:?}: {e}\n{text}"));
        assert_eq!(
            normalize(parsed).to_string(),
            want,
            "{name} with {options:?}"
        );
    }
    let compact = render(value, JsonOptions::default());
    assert!(
        !compact.contains('\n'),
        "{name}: compact output has a line break"
    );
}

#[test]
fn json_fixtures_read_back_as_the_parsed_document() {
    for name in ["sample.json", "nested.json"] {
        let value = tabnas_json::parse(&fixture(name)).unwrap();
        assert_round_trip(name, &value);
    }
}

#[test]
fn the_jsonl_fixture_reads_back_as_the_array_of_its_lines() {
    let value = tabnas_jsonl::parse(&fixture("sample.jsonl")).unwrap();
    assert_round_trip("sample.jsonl", &value);
    assert_eq!(value.to_json().as_array().map(Vec::len), Some(3));
}

#[test]
fn the_yaml_fixture_reads_back_as_the_parsed_document() {
    let value = tabnas_yaml::parse(&fixture("sample.yaml")).unwrap();
    assert_round_trip("sample.yaml", &value);
}

#[test]
fn the_csv_fixture_reads_back_as_the_parsed_records() {
    let value = tabnas_csv::parse(&fixture("sample.csv")).unwrap();
    assert_round_trip("sample.csv", &value);
}

#[test]
fn the_compact_rendering_of_the_json_fixture_is_the_expected_bytes() {
    let value = tabnas_json::parse(&fixture("sample.json")).unwrap();
    assert_eq!(
        render(&value, JsonOptions::default()),
        r#"{"store":{"name":"corner shop","open":true,"books":[{"title":"SICP","price":42.5,"tags":["cs","classic"]},{"title":"TAPL","price":55,"tags":["types"]}],"counts":{"fiction":12,"science":7}},"version":3}"#
    );
}

#[test]
fn the_tsv_fixture_reads_back_through_the_csv_grammars_tab_dialect() {
    let options = tabnas_csv::CsvOptions {
        field: tabnas_csv::FieldOptions {
            separation: Some("\t".into()),
            ..tabnas_csv::FieldOptions::default()
        },
        ..tabnas_csv::CsvOptions::default()
    };
    let value = tabnas_csv::make_with(options)
        .parse(&fixture("sample.tsv"))
        .unwrap();
    assert_round_trip("sample.tsv", &value);
    assert_eq!(
        render(&value, JsonOptions::default()),
        r#"[{"name":"ada","age":"36"},{"name":"lin","age":"28"}]"#
    );
}
