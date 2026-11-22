//! Reading and writing one message (see the crate docs).
use std::io::{self, Read, Write};

/// The largest message either side accepts (an inline image's bytes are
/// the big ones).
pub const MAX_MSG: usize = 256 << 20;

/// One message's bytes, ready to write: header and body together, so a
/// writer shared by threads puts it out whole.
pub fn encode(kind: u8, body: &[u8]) -> Vec<u8> {
    let mut v = Vec::with_capacity(body.len() + 5);
    v.extend_from_slice(&((body.len() + 1) as u32).to_le_bytes());
    v.push(kind);
    v.extend_from_slice(body);
    v
}

pub fn encode_json(v: &serde_json::Value) -> Vec<u8> {
    encode(crate::K_JSON, v.to_string().as_bytes())
}

pub fn write(w: &mut impl Write, kind: u8, body: &[u8]) -> io::Result<()> {
    w.write_all(&encode(kind, body))
}

/// The next message: (kind, body). EOF between messages is UnexpectedEof.
pub fn read(r: &mut impl Read) -> io::Result<(u8, Vec<u8>)> {
    let mut len = [0u8; 4];
    r.read_exact(&mut len)?;
    let len = u32::from_le_bytes(len) as usize;
    if len == 0 || len > MAX_MSG {
        return Err(io::Error::new(io::ErrorKind::InvalidData, format!("keeper: bad message length {len}")));
    }
    let mut kind = [0u8; 1];
    r.read_exact(&mut kind)?;
    let mut body = vec![0u8; len - 1];
    r.read_exact(&mut body)?;
    Ok((kind[0], body))
}

/// A JSON message's object, or an error for any other kind.
pub fn json(kind: u8, body: &[u8]) -> io::Result<serde_json::Value> {
    if kind != crate::K_JSON {
        return Err(io::Error::new(io::ErrorKind::InvalidData, format!("keeper: expected JSON, got kind {kind}")));
    }
    serde_json::from_slice(body).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_message_reads_back() {
        let mut buf = Vec::new();
        super::write(&mut buf, 2, b"frame").unwrap();
        super::write(&mut buf, 1, br#"{"t":"x"}"#).unwrap();
        let mut r = &buf[..];
        assert_eq!(super::read(&mut r).unwrap(), (2, b"frame".to_vec()));
        let (k, b) = super::read(&mut r).unwrap();
        assert_eq!(super::json(k, &b).unwrap()["t"], "x");
        assert!(super::read(&mut r).is_err());
    }

    #[test]
    fn a_bad_length_is_refused() {
        let mut r: &[u8] = &[0, 0, 0, 0, 1];
        assert!(super::read(&mut r).is_err());
    }
}
