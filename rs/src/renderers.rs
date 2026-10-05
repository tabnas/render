//! This crate's implementation of alchemy's `Renderers`: the interface a
//! compiled alchemy program makes its rendering stages through.
//!
//! alchemy's runtime writes a program's output through a [`JsonRenderer`],
//! a [`CsvRenderer`], [`RecordsToJson`], a [`Join`], a [`ReplaceText`] and
//! a [`WriteOut`], and constructs none of them: the host passes
//! [`renderers()`] to `tabnas_alchemy::compile`, and each stage is made
//! through it, exactly as the constructor named would make it.

use std::io::Write;
use std::sync::Arc;

use tabnas_alchemy::shared::{
    CsvOptions, Fail, JoinOut, JsonOptions, Limits, Metrics, Renderers, Sink, TableSink, TextOut,
};

use crate::csv::CsvRenderer;
use crate::json::JsonRenderer;
use crate::records::RecordsToJson;
use crate::text::{Join, ReplaceText, WriteOut};

/// The renderers this crate implements, for alchemy's `Renderers`: every
/// method is the constructor of the same name.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct RenderRenderers;

/// This crate's renderers, to pass to `tabnas_alchemy::compile`.
pub fn renderers() -> RenderRenderers {
    RenderRenderers
}

impl Renderers for RenderRenderers {
    fn json(&self, out: Box<dyn TextOut + Send>, options: JsonOptions) -> Box<dyn Sink + Send> {
        Box::new(JsonRenderer::new(out, options))
    }

    fn csv(
        &self,
        out: Box<dyn TextOut + Send>,
        options: CsvOptions,
    ) -> Result<Box<dyn TableSink + Send>, Fail> {
        Ok(Box::new(CsvRenderer::new(out, options)?))
    }

    fn records_to_json(&self, sink: Box<dyn Sink + Send>) -> Box<dyn TableSink + Send> {
        Box::new(RecordsToJson::new(sink))
    }

    fn join<'a>(
        &self,
        out: Box<dyn TextOut + Send + 'a>,
        separator: &str,
    ) -> Box<dyn JoinOut + Send + 'a> {
        Box::new(Join::new(out, separator))
    }

    fn replace_text<'a>(
        &self,
        out: Box<dyn TextOut + Send + 'a>,
        from: &str,
        to: &str,
    ) -> Box<dyn TextOut + Send + 'a> {
        Box::new(ReplaceText::new(out, from, to))
    }

    fn write_out(
        &self,
        writer: Box<dyn Write + Send>,
        limits: &Limits,
        metrics: Arc<Metrics>,
    ) -> Box<dyn TextOut + Send> {
        Box::new(
            WriteOut::new(writer)
                .with_limits(limits)
                .with_metrics(metrics),
        )
    }
}
