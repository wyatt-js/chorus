//! airplayrelay — chorus's AirPlay 2 sidecar.
//!
//! Mirrors the verb-based CLI of the project's other native sidecars
//! (audiotee, chorusaudio): the Go process spawns this binary and drives it
//! over argv + stdin, never via cgo (CLAUDE.md: "sidecars over cgo").
//!
//!   airplayrelay list [--timeout SECS]
//!       Scan the LAN for AirPlay receivers and print one per line:
//!         <id>\t<name>\t<addr:port>\t<airplay2|airplay1>
//!
//!   airplayrelay render --device <id|name-substring> [--pin CODE]
//!                       [--store DIR] [--timeout SECS]
//!       Connect to the matching receiver and stream raw PCM read from stdin
//!       (s16le / 44100 / stereo — exactly what chorus's broadcaster emits) to
//!       it until stdin reaches EOF.
//!
//! Pairing keys are persisted under <store>/pairings.json so the first run pairs
//! (Pair-Setup) and later runs reconnect (Pair-Verify) without re-pairing.

use std::io::{self, Read};
use std::path::PathBuf;
use std::process::ExitCode;
use std::time::Duration;

use airplay2::audio::{AudioCodec, AudioFormat};
use airplay2::protocol::pairing::storage::FileStorage;
use airplay2::streaming::AudioSource;
use airplay2::{AirPlayClient, AirPlayConfig, AirPlayDevice, scan};

/// Bytes per PCM frame for s16le stereo (2 channels * 2 bytes).
const FRAME_BYTES: usize = 4;

#[tokio::main]
async fn main() -> ExitCode {
    // Logs are off unless RUST_LOG is set (e.g. RUST_LOG=airplay2=debug), so the
    // helper stays quiet on stderr in normal operation.
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_writer(io::stderr)
        .init();

    let mut args = std::env::args().skip(1);
    let cmd = args.next();
    let result = match cmd.as_deref() {
        Some("list") => run_list(args).await,
        Some("render") => run_render(args).await,
        other => {
            eprintln!(
                "airplayrelay: unknown command {:?}\nusage: airplayrelay (list|render) [flags]",
                other.unwrap_or("")
            );
            return ExitCode::from(2);
        }
    };

    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("airplayrelay: {e}");
            ExitCode::FAILURE
        }
    }
}

/// Parsed flags shared by the subcommands.
#[derive(Default)]
struct Flags {
    device: Option<String>,
    pin: Option<String>,
    store: Option<String>,
    timeout: Option<u64>,
}

fn parse_flags(args: impl Iterator<Item = String>) -> Result<Flags, String> {
    let mut f = Flags::default();
    let mut it = args.peekable();
    while let Some(arg) = it.next() {
        let mut take = |name: &str| -> Result<String, String> {
            it.next()
                .ok_or_else(|| format!("flag {name} needs a value"))
        };
        match arg.as_str() {
            "--device" | "-d" => f.device = Some(take("--device")?),
            "--pin" => f.pin = Some(take("--pin")?),
            "--store" => f.store = Some(take("--store")?),
            "--timeout" => {
                f.timeout = Some(
                    take("--timeout")?
                        .parse()
                        .map_err(|_| "--timeout must be whole seconds".to_string())?,
                )
            }
            other => return Err(format!("unknown flag {other}")),
        }
    }
    Ok(f)
}

async fn run_list(args: impl Iterator<Item = String>) -> Result<(), Box<dyn std::error::Error>> {
    let flags = parse_flags(args)?;
    let timeout = Duration::from_secs(flags.timeout.unwrap_or(5));

    let devices = scan(timeout).await?;
    let mut out = String::new();
    for d in &devices {
        // Only audio-capable receivers are useful to chorus.
        if !d.capabilities.supports_audio {
            continue;
        }
        let addr = d
            .addresses
            .first()
            .map_or_else(|| "?".to_string(), |ip| format!("{ip}:{}", d.port));
        let proto = if d.capabilities.airplay2 {
            "airplay2"
        } else {
            "airplay1"
        };
        out.push_str(&format!("{}\t{}\t{}\t{}\n", d.id, d.name, addr, proto));
    }
    print!("{out}");
    Ok(())
}

async fn run_render(args: impl Iterator<Item = String>) -> Result<(), Box<dyn std::error::Error>> {
    let flags = parse_flags(args)?;
    let want = flags
        .device
        .ok_or("render: --device <id|name-substring> is required")?;
    let timeout = Duration::from_secs(flags.timeout.unwrap_or(5));

    eprintln!("airplayrelay: scanning for {want:?}...");
    let devices = scan(timeout).await?;
    let device = pick_device(&devices, &want)?;
    eprintln!(
        "airplayrelay: connecting to {} ({})",
        device.name, device.id
    );

    let mut config = AirPlayConfig::default();
    // ALAC (Apple Lossless) is the universal AirPlay 2 codec; the crate defaults to
    // raw PCM, which strict receivers reject. The streamer encodes stdin PCM -> ALAC.
    config.audio_codec = AudioCodec::Alac;
    config.pin = flags.pin.clone();
    let store_path = pairings_path(flags.store.as_deref());
    config.pairing_storage_path = Some(store_path.clone());

    let storage = Box::new(FileStorage::new(&store_path, None).await?);
    let mut client = AirPlayClient::new(config).with_pairing_storage(storage);

    client.connect(&device).await?;
    eprintln!("airplayrelay: connected; streaming PCM from stdin (Ctrl-D / pipe-close to stop)");

    let source = StdinSource::new();
    client.stream_audio(source).await?;

    eprintln!("airplayrelay: stream ended, disconnecting");
    client.disconnect().await?;
    Ok(())
}

/// Match a discovered device by exact id, else case-insensitive name substring.
fn pick_device<'a>(
    devices: &'a [AirPlayDevice],
    want: &str,
) -> Result<AirPlayDevice, Box<dyn std::error::Error>> {
    if let Some(d) = devices.iter().find(|d| d.id == want) {
        return Ok(d.clone());
    }
    let needle = want.to_lowercase();
    let matches: Vec<&AirPlayDevice> = devices
        .iter()
        .filter(|d| d.name.to_lowercase().contains(&needle))
        .collect();
    match matches.as_slice() {
        [] => Err(format!("no AirPlay device matching {want:?} (try `airplayrelay list`)").into()),
        [d] => Ok((*d).clone()),
        many => Err(format!(
            "{want:?} matches {} devices ({}); be more specific",
            many.len(),
            many.iter()
                .map(|d| d.name.as_str())
                .collect::<Vec<_>>()
                .join(", ")
        )
        .into()),
    }
}

/// Default pairing-store file: <store-dir>/pairings.json, defaulting the dir to
/// ~/Library/Application Support/chorus/airplay.
fn pairings_path(store: Option<&str>) -> PathBuf {
    let dir = store.map(PathBuf::from).unwrap_or_else(|| {
        let home = std::env::var_os("HOME").map_or_else(|| PathBuf::from("."), PathBuf::from);
        home.join("Library/Application Support/chorus/airplay")
    });
    dir.join("pairings.json")
}

/// An [`AudioSource`] backed by this process's stdin. chorus pipes s16le /
/// 44100 / stereo PCM in; we hand it to the streamer unchanged (the AirPlay
/// PcmStreamer owns the on-wire format conversion).
struct StdinSource {
    stdin: io::Stdin,
}

impl StdinSource {
    fn new() -> Self {
        Self { stdin: io::stdin() }
    }
}

impl AudioSource for StdinSource {
    fn format(&self) -> AudioFormat {
        AudioFormat::CD_QUALITY // s16le / 44100 / stereo
    }

    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        if buffer.len() < FRAME_BYTES {
            return Ok(0);
        }
        // One blocking read provides natural backpressure: the streamer pulls
        // only as fast as chorus feeds us. Returning 0 signals EOF (pipe
        // closed), which ends the stream cleanly.
        let mut filled = match read_once(&mut self.stdin, buffer)? {
            0 => return Ok(0),
            n => n,
        };
        // A pipe read may split a frame; top up so we always return a whole
        // number of frames (otherwise L/R channels would swap downstream).
        while filled % FRAME_BYTES != 0 {
            match read_once(&mut self.stdin, &mut buffer[filled..])? {
                0 => {
                    filled -= filled % FRAME_BYTES; // EOF mid-frame: drop the stub
                    break;
                }
                n => filled += n,
            }
        }
        Ok(filled)
    }
}

/// A single read that transparently retries on EINTR.
fn read_once(stdin: &mut io::Stdin, buf: &mut [u8]) -> io::Result<usize> {
    loop {
        match stdin.read(buf) {
            Err(e) if e.kind() == io::ErrorKind::Interrupted => continue,
            other => return other,
        }
    }
}
