use anyhow::{bail, Context, Result};
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

pub const ALPN: &[u8] = b"suzuri-workspace/0";

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireMsg {
    pub v: u32,
    pub channel: String,
    pub id: String,
    pub line: String,
}

pub async fn write_msg<W: AsyncWrite + Unpin>(w: &mut W, msg: &WireMsg) -> Result<()> {
    let buf = serde_json::to_vec(msg)?;
    if buf.len() > 8 * 1024 * 1024 {
        bail!("message too large");
    }
    w.write_u32_le(buf.len() as u32).await?;
    w.write_all(&buf).await?;
    w.flush().await?;
    Ok(())
}

pub async fn read_msg<R: AsyncRead + Unpin>(r: &mut R) -> Result<Option<WireMsg>> {
    let len = match r.read_u32_le().await {
        Ok(n) => n as usize,
        Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(e) => return Err(e.into()),
    };
    if len == 0 {
        return Ok(None);
    }
    if len > 8 * 1024 * 1024 {
        bail!("frame too large: {len}");
    }
    let mut buf = vec![0u8; len];
    r.read_exact(&mut buf).await.context("read frame")?;
    let msg = serde_json::from_slice(&buf).context("parse wire msg")?;
    Ok(Some(msg))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wire_msg_v0_roundtrip_json() {
        let msg = WireMsg {
            v: 0,
            channel: "general".into(),
            id: "msg_1".into(),
            line: r#"{"id":"msg_1"}"#.into(),
        };
        let buf = serde_json::to_vec(&msg).unwrap();
        let got: WireMsg = serde_json::from_slice(&buf).unwrap();
        assert_eq!(got.v, 0);
        assert_eq!(got.id, "msg_1");
    }
}
