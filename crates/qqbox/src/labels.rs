use std::time::{SystemTime, UNIX_EPOCH};

use serde_json::{json, Map, Value};

use crate::error::{Error, Result};
use crate::types::{validate_label_key, validate_label_value, LabelMap};

pub fn validate(k: &str, v: &str) -> Result<()> {
    validate_label_key(k)?;
    validate_label_value(v)?;
    if k.contains('!') {
        return Err(Error::Config(format!(
            "label key has an unsupported negation: {}",
            k
        )));
    }
    Ok(())
}

pub fn parse_filter(s: &str) -> Result<LabelMap> {
    let mut filters = LabelMap::new();
    let trimmed = s.trim();
    if trimmed.is_empty() {
        return Ok(filters);
    }
    for expression in split_expressions(trimmed) {
        let expression = expression.trim();
        if expression.is_empty() {
            continue;
        }
        let body = match expression.strip_prefix("label=") {
            Some(rest) => rest,
            None => expression,
        };
        let (key, value) = match body.split_once('=') {
            Some((key, value)) => (key, value),
            None => (body, ""),
        };
        validate(key, value)?;
        filters.insert(key.to_owned(), value.to_owned());
    }
    Ok(filters)
}

pub fn matches(labels: &LabelMap, filter: &LabelMap) -> bool {
    crate::types::labels_match(labels, filter)
}

pub fn container_metadata(name: &str, image: &str, labels: &LabelMap) -> Value {
    let mut valid = Map::new();
    for (key, value) in labels {
        if validate(key, value).is_ok() {
            valid.insert(key.clone(), Value::String(value.clone()));
        }
    }
    let display_name = if name.starts_with('/') {
        name.to_owned()
    } else {
        format!("/{}", name)
    };
    let labels_value = Value::Object(valid);
    json!({
        "Id": identity(name, image),
        "Name": display_name,
        "Image": image,
        "Created": utc_timestamp(),
        "Labels": labels_value.clone(),
        "Config": {
            "Image": image,
            "Labels": labels_value,
        },
        "State": {
            "Status": "created",
        },
    })
}

fn split_expressions(input: &str) -> Vec<String> {
    let mut parts: Vec<String> = Vec::new();
    for segment in input.split(',') {
        let previous_has_value = parts
            .last()
            .map(|previous| previous.contains('='))
            .unwrap_or(false);
        let starts_new =
            segment.starts_with("label=") || segment.contains('=') || !previous_has_value;
        if parts.is_empty() || starts_new {
            parts.push(segment.to_owned());
        } else if let Some(previous) = parts.last_mut() {
            previous.push(',');
            previous.push_str(segment);
        }
    }
    parts
}

fn identity(name: &str, image: &str) -> String {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    let separator = [0u8];
    for byte in name
        .as_bytes()
        .iter()
        .chain(separator.iter())
        .chain(image.as_bytes().iter())
    {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    format!("{:016x}", hash)
}

fn utc_timestamp() -> String {
    let secs = match SystemTime::now().duration_since(UNIX_EPOCH) {
        Ok(duration) => duration.as_secs() as i64,
        Err(_) => 0,
    };
    let days = secs.div_euclid(86_400);
    let within = secs.rem_euclid(86_400);
    let (year, month, day) = civil_from_days(days);
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z",
        year,
        month,
        day,
        within / 3600,
        (within % 3600) / 60,
        within % 60
    )
}

fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let shifted = days + 719_468;
    let era = if shifted >= 0 {
        shifted
    } else {
        shifted - 146_096
    } / 146_097;
    let day_of_era = shifted - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_prime + 2) / 5 + 1;
    let month = month_prime + if month_prime < 10 { 3 } else { -9 };
    (year + if month <= 2 { 1 } else { 0 }, month, day)
}
