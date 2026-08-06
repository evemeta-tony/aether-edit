// Uploader.jsx — resumable, chunked, multi-threaded ingest for the file transcoder.
// The model here is the one a real large-file mover uses: the source is split into
// fixed-size chunks, N transfer threads each claim a chunk, failed chunks go back
// to the pool and are retried, and a connection break resumes from the chunk map
// rather than from byte zero. Everything below is a faithful simulation of that
// state machine so the UI can be spec'd before the transport exists.
const { Panel, Dot, Read, Meter, TChip, Eb } = window;

const THREADS = 8;          // parallel transfer streams
const CHUNK_MB = 64;        // chunk size on the wire
const TICK = 350;           // ms

const PENDING = 0, INFLIGHT = 1, DONE = 2, RETRY = 3;

const chunkCount = sizeGB => Math.max(24, Math.min(180, Math.round((sizeGB * 1024) / CHUNK_MB)));

const mkUpload = ({ name, sizeGB }) => {
  const n = chunkCount(sizeGB);
  return {
    id: 'u' + Math.random().toString(36).slice(2, 8),
    file: name, size: sizeGB,
    chunks: Array(n).fill(PENDING),
    streams: Array(THREADS).fill(null),
    state: 'uploading', rate: 0, retries: 0, elapsed: 0, at: 0,
  };
};

function useUploads(onLanded) {
  const [uploads, setUploads] = React.useState([]);
  const landedRef = React.useRef(new Set());

  React.useEffect(() => {
    const iv = setInterval(() => {
      setUploads(prev => prev.map(u => {
        if (u.state === 'error') return { ...u, at: u.at + TICK, state: u.at > 2400 ? 'uploading' : 'error', ...(u.at > 2400 ? { at: 0 } : {}) };
        if (u.state === 'verifying') {
          if (u.at > 1600) return { ...u, state: 'done', at: 0, rate: 0 };
          return { ...u, at: u.at + TICK };
        }
        if (u.state !== 'uploading') return u;

        const chunks = u.chunks.slice();
        const streams = u.streams.slice();
        let finished = 0, retries = u.retries;

        // resolve in-flight chunks
        for (let s = 0; s < THREADS; s++) {
          const c = streams[s];
          if (c == null) continue;
          if (Math.random() < 0.035) { chunks[c] = RETRY; streams[s] = null; retries++; continue; }
          if (Math.random() < 0.5) { chunks[c] = DONE; streams[s] = null; finished++; }
        }
        // retried chunks return to the pool
        for (let i = 0; i < chunks.length; i++) if (chunks[i] === RETRY && Math.random() < 0.6) chunks[i] = PENDING;
        // claim new work
        for (let s = 0; s < THREADS; s++) {
          if (streams[s] != null) continue;
          const i = chunks.indexOf(PENDING);
          if (i < 0) break;
          chunks[i] = INFLIGHT; streams[s] = i;
        }

        const open = chunks.some(c => c !== DONE);
        // Wire throughput is what the threads sustain, not how fast the sim paints:
        // each live stream moves ~80-130 MB/s, which is what 8 threads do on a 10G path.
        const live = streams.filter(s => s != null).length;
        return {
          ...u, chunks, streams, retries,
          elapsed: u.elapsed + TICK,
          rate: live ? live * (80 + Math.random() * 50) : 0,
          state: open ? 'uploading' : 'verifying',
          at: open ? 0 : 0,
        };
      }));
    }, TICK);
    return () => clearInterval(iv);
  }, []);

  React.useEffect(() => {
    uploads.filter(u => u.state === 'done' && !landedRef.current.has(u.id)).forEach(u => {
      landedRef.current.add(u.id);
      onLanded(u);
    });
  }, [uploads, onLanded]);

  return {
    uploads,
    add: files => setUploads(p => [...p, ...files.map(mkUpload)]),
    pause: id => setUploads(p => p.map(u => u.id === id ? { ...u, state: 'paused', streams: Array(THREADS).fill(null), chunks: u.chunks.map(c => c === INFLIGHT ? PENDING : c), rate: 0 } : u)),
    resume: id => setUploads(p => p.map(u => u.id === id ? { ...u, state: 'uploading' } : u)),
    breakLink: id => setUploads(p => p.map(u => u.id === id ? { ...u, state: 'error', at: 0, rate: 0, streams: Array(THREADS).fill(null), chunks: u.chunks.map(c => c === INFLIGHT ? PENDING : c) } : u)),
    cancel: id => setUploads(p => p.filter(u => u.id !== id)),
  };
}

const U_STATE = {
  uploading: ['ok', 'Transferring'],
  paused: ['idle', 'Paused'],
  error: ['err', 'Link lost · resuming'],
  verifying: ['warn', 'Verifying checksum'],
  done: ['ok', 'Landed'],
};

function ChunkMap({ chunks }) {
  const color = c => c === DONE ? 'var(--blue-500)' : c === INFLIGHT ? 'var(--blue-300)' : c === RETRY ? 'var(--warn)' : 'rgba(255,255,255,.07)';
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 2, alignContent: 'flex-start' }}>
      {chunks.map((c, i) => <span key={i} style={{ width: 8, height: 8, borderRadius: 1, background: color(c), flex: 'none' }} />)}
    </div>
  );
}

function UBtn({ icon, children, onClick, danger }) {
  return (
    <button className="ae-b" onClick={onClick} style={{
      display: 'inline-flex', alignItems: 'center', gap: 6, font: '600 10px var(--font-sans)',
      letterSpacing: '.09em', textTransform: 'uppercase', padding: '6px 9px', borderRadius: 'var(--r-xs)',
      cursor: 'pointer', background: 'transparent', border: '1px solid var(--line-strong)',
      color: danger ? 'var(--err)' : 'var(--fg1)', whiteSpace: 'nowrap',
    }}>{icon && <Icon name={icon} size={11} />}{children}</button>
  );
}

function TransferPanel({ uploads, sel, onSel, pause, resume, breakLink, cancel }) {
  const u = uploads.find(x => x.id === sel) || uploads.find(x => x.state !== 'done') || uploads[0];
  if (!u) {
    return (
      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 8, color: 'var(--fg3)' }}>
        <Icon name="upload-cloud" size={20} color="var(--fg4)" />
        <span style={{ font: 'var(--t-body-sm)' }}>Drop media onto the job queue to start a transfer</span>
        <span style={{ font: 'var(--t-micro)', color: 'var(--fg4)' }}>{CHUNK_MB} MB chunks · {THREADS} parallel streams · resumes on break</span>
      </div>
    );
  }
  const [tone, label] = U_STATE[u.state];
  const done = u.chunks.filter(c => c === DONE).length;
  const pct = (done / u.chunks.length) * 100;
  // The map is clamped for legibility, so a cell stands for a span of the file.
  const perCell = (u.size * 1024) / u.chunks.length;
  const moved = (done * perCell) / 1024;
  const eta = u.rate > 0 ? ((u.size - moved) * 1024) / u.rate : null;
  const cellLabel = perCell >= 1024 ? `${(perCell / 1024).toFixed(1)} GB` : `${Math.round(perCell)} MB`;

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', gap: 11, padding: '11px 13px', overflow: 'hidden' }}>
      {uploads.length > 1 && (
        <div style={{ display: 'flex', gap: 6, overflowX: 'auto', flex: 'none' }}>
          {uploads.map(x => <TChip key={x.id} active={x.id === u.id} onClick={() => onSel(x.id)}>{x.file.slice(0, 18)}</TChip>)}
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flex: 'none' }}>
        <Dot tone={tone} pulse={u.state === 'uploading'} />
        <span style={{ font: '400 12px var(--font-mono)', color: '#fff', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{u.file}</span>
        <span style={{ font: 'var(--t-micro)', color: 'var(--fg3)', whiteSpace: 'nowrap' }}>{label}</span>
        <div style={{ flex: 1 }} />
        {u.state === 'uploading' && <UBtn icon="pause" onClick={() => pause(u.id)}>Pause</UBtn>}
        {(u.state === 'paused' || u.state === 'error') && <UBtn icon="play" onClick={() => resume(u.id)}>Resume</UBtn>}
        {u.state === 'uploading' && <UBtn icon="unplug" onClick={() => breakLink(u.id)}>Drop link</UBtn>}
        <UBtn icon="x" danger onClick={() => cancel(u.id)}>Cancel</UBtn>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flex: 'none' }}>
        <span style={{ font: '400 12px var(--font-mono)', color: '#fff', width: 44, fontVariantNumeric: 'tabular-nums' }}>{pct.toFixed(0)}%</span>
        <div style={{ flex: 1 }}><Meter pct={pct} h={4} color={u.state === 'error' ? 'var(--err)' : u.state === 'verifying' ? 'var(--warn)' : 'var(--blue-500)'} /></div>
        <span style={{ font: '400 11px var(--font-mono)', color: 'var(--fg3)', whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>{moved.toFixed(1)} / {u.size.toFixed(1)} GB</span>
      </div>

      <div style={{ display: 'flex', gap: 20, flex: 1, minHeight: 0 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, flex: 'none', width: 150 }}>
          <div style={{ display: 'flex', gap: 18 }}>
            <Read value={u.rate ? (u.rate).toFixed(0) : '0'} unit="MB/s" label="Throughput" size={17} />
            <Read value={eta ? (eta > 60 ? `${Math.floor(eta / 60)}m` : `${Math.max(0, Math.floor(eta))}s`) : '—'} label="Eta" size={17} />
          </div>
          <div style={{ display: 'flex', gap: 18 }}>
            <Read value={String(u.retries)} label="Chunk retries" size={17} tone={u.retries > 6 ? 'var(--warn)' : undefined} />
            <Read value={`${done}/${u.chunks.length}`} label="Chunks" size={17} />
          </div>
        </div>

        <div style={{ flex: 'none', width: 176, display: 'flex', flexDirection: 'column', gap: 5, minHeight: 0 }}>
          <Eb>Streams · {u.streams.filter(s => s != null).length}/{THREADS}</Eb>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '5px 10px' }}>
            {u.streams.map((c, i) => (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <span style={{ font: '400 9px var(--font-mono)', color: 'var(--fg4)', width: 14 }}>{String(i + 1).padStart(2, '0')}</span>
                <div style={{ flex: 1 }}><Meter pct={c == null ? 0 : 30 + ((i * 37 + (u.elapsed / TICK)) % 70)} h={2} color={c == null ? 'var(--idle)' : 'var(--viz-2)'} /></div>
              </div>
            ))}
          </div>
        </div>

        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 5 }}>
          <Eb>Chunk map · {cellLabel} per cell</Eb>
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto' }}><ChunkMap chunks={u.chunks} /></div>
        </div>
      </div>
    </div>
  );
}

// Full-bleed drop target overlay shown while a file is dragged over the queue.
function DropVeil({ over }) {
  if (!over) return null;
  return (
    <div style={{
      position: 'absolute', inset: 0, zIndex: 5, display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center', gap: 10, pointerEvents: 'none',
      background: 'rgba(10,16,32,.86)', border: '1px dashed var(--blue-500)', borderRadius: 'var(--r-md)',
    }}>
      <Icon name="upload-cloud" size={26} color="var(--blue-300)" />
      <span style={{ font: 'var(--t-h3)', letterSpacing: 'var(--ls-head)', color: '#fff' }}>Drop media to upload</span>
      <span style={{ font: 'var(--t-micro)', color: 'var(--fg3)' }}>{CHUNK_MB} MB chunks · {THREADS} parallel streams · resumable</span>
    </div>
  );
}

Object.assign(window, { useUploads, TransferPanel, DropVeil, THREADS, CHUNK_MB });
