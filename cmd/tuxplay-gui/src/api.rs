use std::io::{Read, Write};
use std::os::unix::net::UnixStream;

use anyhow::{anyhow, Context, Result};
use serde_json::{json, Value};

use crate::model::Status;

#[derive(Clone, Debug)]
pub struct Client {
    socket_path: String,
}

impl Client {
    pub fn new(socket_path: impl Into<String>) -> Self {
        Self {
            socket_path: socket_path.into(),
        }
    }

    pub fn status(&self) -> Result<Status> {
        self.request("GET", "/v1/status", None)
    }

    pub fn route(&self, device: &str, add: bool) -> Result<()> {
        let _: Value = self.request("POST", "/v1/route", Some(json!({ "device": device, "add": add })))?;
        Ok(())
    }

    pub fn unroute(&self, device: &str) -> Result<()> {
        let _: Value = self.request("POST", "/v1/unroute", Some(json!({ "device": device })))?;
        Ok(())
    }

    pub fn volume(&self, device: &str, percent: i32) -> Result<()> {
        let _: Value = self.request(
            "POST",
            "/v1/volume",
            Some(json!({ "device": device, "percent": percent })),
        )?;
        Ok(())
    }

    fn request<T: serde::de::DeserializeOwned>(
        &self,
        method: &str,
        path: &str,
        body: Option<Value>,
    ) -> Result<T> {
        let mut stream =
            UnixStream::connect(&self.socket_path).with_context(|| format!("connect {}", self.socket_path))?;
        stream
            .set_read_timeout(Some(std::time::Duration::from_secs(5)))
            .ok();
        stream
            .set_write_timeout(Some(std::time::Duration::from_secs(5)))
            .ok();

        let payload = body.map(|value| serde_json::to_vec(&value)).transpose()?;
        let mut request = format!(
            "{method} {path} HTTP/1.1\r\nHost: unix\r\nConnection: close\r\n"
        );
        if let Some(payload) = &payload {
            request.push_str("Content-Type: application/json\r\n");
            request.push_str(&format!("Content-Length: {}\r\n", payload.len()));
        }
        request.push_str("\r\n");

        stream.write_all(request.as_bytes())?;
        if let Some(payload) = &payload {
            stream.write_all(payload)?;
        }
        stream.flush()?;

        let mut response = Vec::new();
        stream.read_to_end(&mut response)?;
        let response = String::from_utf8(response).context("decode daemon response as UTF-8")?;
        let (head, body) = response
            .split_once("\r\n\r\n")
            .ok_or_else(|| anyhow!("invalid daemon response"))?;
        let headers = parse_headers(head);
        let body = if is_chunked(&headers) {
            decode_chunked_body(body)?
        } else {
            body.to_string()
        };

        let status_line = head.lines().next().ok_or_else(|| anyhow!("missing HTTP status line"))?;
        let status_code = status_line
            .split_whitespace()
            .nth(1)
            .ok_or_else(|| anyhow!("missing HTTP status code"))?
            .parse::<u16>()
            .context("parse HTTP status code")?;

        if status_code >= 400 {
            let value: Value = serde_json::from_str(&body).context("decode daemon error body")?;
            let message = value
                .get("error")
                .and_then(Value::as_str)
                .unwrap_or("daemon request failed");
            return Err(anyhow!(message.to_string()));
        }

        serde_json::from_str(&body).context("decode daemon JSON body")
    }
}

fn parse_headers(head: &str) -> Vec<(String, String)> {
    head.lines()
        .skip(1)
        .filter_map(|line| {
            let (name, value) = line.split_once(':')?;
            Some((name.trim().to_ascii_lowercase(), value.trim().to_string()))
        })
        .collect()
}

fn is_chunked(headers: &[(String, String)]) -> bool {
    headers.iter().any(|(name, value)| {
        name == "transfer-encoding" && value.to_ascii_lowercase().contains("chunked")
    })
}

fn decode_chunked_body(body: &str) -> Result<String> {
    let mut rest = body;
    let mut decoded = String::new();

    loop {
        let (size_line, after_size) = rest
            .split_once("\r\n")
            .ok_or_else(|| anyhow!("invalid chunked response"))?;
        let size_hex = size_line.split(';').next().unwrap_or(size_line).trim();
        let size = usize::from_str_radix(size_hex, 16).context("parse chunk size")?;
        if size == 0 {
            break;
        }

        if after_size.len() < size + 2 {
            return Err(anyhow!("truncated chunked response"));
        }

        decoded.push_str(&after_size[..size]);
        rest = &after_size[size + 2..];
    }

    Ok(decoded)
}
