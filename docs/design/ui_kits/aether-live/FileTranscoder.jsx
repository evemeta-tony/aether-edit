// FileTranscoder.jsx — Aether Cloud · File transcoder. The batch/VOD counterpart
// to the live console: a farm chewing through a queue of source files instead of
// one bonded ingest. Jobs advance on a 500ms tick, workers are claimed and
// released, failures can be retried, and the output profile edits the selection.
const { stamp, Panel, Dot, Read, Meter, TChip, DragSlider, Row, Eb, Graph, ConsoleCrumb, UserMenu } = window;
const { useUploads, TransferPanel, DropVeil, THREADS, CHUNK_MB } = window;

// Titles offered when a drop carries no real files (preview affordance).
const NEXT_FILES = [
  { name: 'S04_ep05_master.mxf', sizeGB: 139.4 },
  { name: 'launch_film_grade_v3.mov', sizeGB: 62.8 },
  { name: 'crowd_cam_b_raw.mxf', sizeGB: 96.1 },
];

// EVE is the automated path: analysis, per-title ladder and packaging are decided
// upstream. The operator only chooses which delivery formats come out the far end.
const EVE_STEPS = [
  ['scan-line', 'Source analysis', 'grain, motion, cadence'],
  ['layers', 'Per-title ladder', 'rungs fitted to content'],
  ['gauge', 'Quality target', 'VMAF 94 held per rung'],
  ['package', 'Packaging', 'segmented + encrypted'],
];
const EVE_FORMATS = ['HLS', 'DASH', 'MP4', 'CMAF'];

const PRESETS = {
  web1080: { name: 'Web 1080p', container: 'MP4', codec: 'H.264', w: 1920, h: 1080, fps: 25, rc: 'CRF', crf: 21, target: 8.0, gop: 2, speed: 'medium', audio: 'AAC 192 kb/s' },
  hls4k:   { name: 'HLS ladder 4K', container: 'HLS', codec: 'HEVC', w: 3840, h: 2160, fps: 25, rc: 'VBR', crf: 24, target: 16.0, gop: 4, speed: 'slow', audio: 'AAC 256 kb/s' },
  proxy:   { name: 'Editorial proxy', container: 'MOV', codec: 'ProRes LT', w: 1280, h: 720, fps: 25, rc: 'CBR', crf: 18, target: 22.0, gop: 1, speed: 'fast', audio: 'PCM 24-bit' },
  social:  { name: 'Social 9:16', container: 'MP4', codec: 'H.264', w: 1080, h: 1920, fps: 30, rc: 'CRF', crf: 23, target: 6.0, gop: 2, speed: 'medium', audio: 'AAC 128 kb/s' },
};

const SRC = {
  a: { container: 'MXF · OP1a', codec: 'XAVC 10-bit', w: 3840, h: 2160, fps: 25, rate: 412, chroma: '4:2:2 · 10-bit', audio: 4, subs: 2 },
  b: { container: 'MOV · QuickTime', codec: 'ProRes 422 HQ', w: 1920, h: 1080, fps: 25, rate: 176, chroma: '4:2:2 · 10-bit', audio: 2, subs: 0 },
  c: { container: 'MP4 · ISO', codec: 'H.264 High', w: 1920, h: 1080, fps: 30, rate: 24, chroma: '4:2:0 · 8-bit', audio: 2, subs: 1 },
  d: { container: 'MXF · OP1a', codec: 'HEVC 10-bit', w: 3840, h: 2160, fps: 50, rate: 288, chroma: '4:2:0 · 10-bit', audio: 8, subs: 3 },
};

const INITIAL_JOBS = [
  { id: 'j1', file: 'S04_ep02_master.mxf',      src: 'a', preset: 'hls4k',   dur: 2748, size: 141.6, state: 'done',    pct: 100, speed: 3.1 },
  { id: 'j2', file: 'S04_ep03_master.mxf',      src: 'a', preset: 'hls4k',   dur: 2691, size: 138.7, state: 'running', pct: 62,  speed: 2.8 },
  { id: 'j3', file: 'promo_60_v7.mov',          src: 'b', preset: 'web1080', dur: 60,   size: 1.32,  state: 'running', pct: 24,  speed: 11.4 },
  { id: 'j4', file: 'keynote_stage_cam.mxf',    src: 'd', preset: 'hls4k',   dur: 5412, size: 194.2, state: 'running', pct: 8,   speed: 1.6 },
  { id: 'j5', file: 'behind_the_scenes_04.mp4', src: 'c', preset: 'social',  dur: 388,  size: 1.14,  state: 'queued',  pct: 0,   speed: 0 },
  { id: 'j6', file: 'S04_ep04_master.mxf',      src: 'a', preset: 'proxy',   dur: 2802, size: 144.9, state: 'queued',  pct: 0,   speed: 0 },
  { id: 'j7', file: 'sizzle_cutdown_30.mov',    src: 'b', preset: 'social',  dur: 30,   size: 0.66,  state: 'queued',  pct: 0,   speed: 0 },
  { id: 'j8', file: 'panel_room_b_raw.mxf',     src: 'd', preset: 'web1080', dur: 4980, size: 178.4, state: 'failed',  pct: 41,  speed: 0, err: 'audio stream 6 · unsupported layout' },
  { id: 'j9', file: 'archive_1998_telecine.mov', src: 'b', preset: 'web1080', dur: 1620, size: 46.3, state: 'queued',  pct: 0,   speed: 0 },
];

const WORKERS = [
  { id: 'w1', name: 'FARM-01', gpu: 'A10G ×2', region: 'us-east' },
  { id: 'w2', name: 'FARM-02', gpu: 'A10G ×2', region: 'us-east' },
  { id: 'w3', name: 'FARM-03', gpu: 'L4 ×4',   region: 'eu-west' },
  { id: 'w4', name: 'FARM-04', gpu: 'L4 ×4',   region: 'eu-west' },
];

const FT_LOG = [
  ['scan', 'probe S04_ep03_master.mxf · 4 audio · 2 subtitle'],
  ['enc', 'FARM-02 hevc_nvenc pass 2/2 · 2.8× realtime'],
  ['pkg', 'hls variant 2160p written · 412 segments'],
  ['io', 's3://aether-media/in · 141.6 GB read · 940 MB/s'],
  ['enc', 'FARM-01 h264_nvenc crf 21 · 11.4× realtime'],
  ['job', 'S04_ep02_master.mxf complete in 14m 47s'],
  ['warn', 'panel_room_b_raw.mxf · audio stream 6 unsupported layout'],
  ['out', 'media library · 3 renditions registered'],
  ['sys', 'farm 3/4 nodes claimed · queue depth 4'],
];

const fmtDur = s => `${String(Math.floor(s / 3600)).padStart(2, '0')}:${String(Math.floor(s / 60) % 60).padStart(2, '0')}:${String(Math.floor(s % 60)).padStart(2, '0')}`;
const fmtEta = s => s >= 3600 ? `${Math.floor(s / 3600)}h ${Math.floor(s / 60) % 60}m` : s >= 60 ? `${Math.floor(s / 60)}m ${Math.floor(s % 60)}s` : `${Math.max(0, Math.floor(s))}s`;
const STATE = { done: ['ok', 'Complete'], running: ['onair', 'Encoding'], queued: ['idle', 'Queued'], failed: ['err', 'Failed'] };
const MAX_CONCURRENT = 3;

function FileTranscoder() {
  const [jobs, setJobs] = React.useState(INITIAL_JOBS);
  const [profiles, setProfiles] = React.useState(PRESETS);
  const [eve, setEve] = React.useState(false);
  const [formats, setFormats] = React.useState(['HLS', 'DASH']);
  const [bottom, setBottom] = React.useState('log');
  const [over, setOver] = React.useState(false);
  const [xfer, setXfer] = React.useState(null);
  const [sel, setSel] = React.useState('j2');
  const [running, setRunning] = React.useState(true);
  const [filter, setFilter] = React.useState('all');
  const [hist, setHist] = React.useState(() => Array.from({ length: 60 }, () => 700 + Math.random() * 160));
  const [log, setLog] = React.useState(() => FT_LOG.slice(0, 6).map((l, i) => ({ id: i, t: stamp(), k: l[0], m: l[1] })));
  const logRef = React.useRef(null);

  // A landed transfer becomes a queued transcode job.
  const landed = React.useCallback(u => {
    setJobs(p => [...p, { id: u.id, file: u.file, src: 'a', preset: 'web1080', dur: Math.round(u.size * 32), size: u.size, state: 'queued', pct: 0, speed: 0 }]);
    setLog(p => [...p.slice(-40), { id: Math.random(), t: stamp(), k: 'io', m: `${u.file} landed · checksum verified · queued` }]);
  }, []);
  const { uploads, add, pause, resume, breakLink, cancel } = useUploads(landed);

  const onDrop = e => {
    e.preventDefault(); setOver(false);
    const fs = Array.from((e.dataTransfer && e.dataTransfer.files) || []);
    const picked = fs.length
      ? fs.map(f => ({ name: f.name, sizeGB: Math.max(0.4, f.size / 1073741824) }))
      : [NEXT_FILES[uploads.length % NEXT_FILES.length]];
    add(picked); setBottom('xfer'); setXfer(null);
  };
  const addMedia = () => { add([NEXT_FILES[uploads.length % NEXT_FILES.length]]); setBottom('xfer'); setXfer(null); };

  const active = jobs.filter(j => j.state === 'running');
  const queued = jobs.filter(j => j.state === 'queued');
  const done = jobs.filter(j => j.state === 'done');
  const failed = jobs.filter(j => j.state === 'failed');
  const fps = active.reduce((n, j) => n + j.speed * 25, 0);

  React.useEffect(() => {
    const iv = setInterval(() => {
      setHist(h => [...h.slice(1), running ? Math.max(0, fps + (Math.random() - 0.5) * 80) : 0]);
      if (!running) return;
      setJobs(prev => {
        let next = prev.map(j => {
          if (j.state !== 'running') return j;
          const step = (j.speed * 0.5) / j.dur * 100;
          const pct = j.pct + step;
          return pct >= 100 ? { ...j, pct: 100, state: 'done', speed: 0 } : { ...j, pct };
        });
        const slots = MAX_CONCURRENT - next.filter(j => j.state === 'running').length;
        if (slots > 0) {
          let filled = 0;
          next = next.map(j => {
            if (j.state === 'queued' && filled < slots) {
              filled++;
              return { ...j, state: 'running', speed: +(1.4 + Math.random() * 9).toFixed(1) };
            }
            return j;
          });
        }
        return next;
      });
      if (Math.random() < 0.5) {
        const l = FT_LOG[Math.floor(Math.random() * FT_LOG.length)];
        setLog(p => [...p.slice(-40), { id: Math.random(), t: stamp(), k: l[0], m: l[1] }]);
      }
    }, 500);
    return () => clearInterval(iv);
  }, [running, fps]);

  React.useEffect(() => { const el = logRef.current; if (el) el.scrollTop = el.scrollHeight; }, [log]);

  const job = jobs.find(j => j.id === sel) || jobs[0];
  const src = SRC[job.src];
  const p = profiles[job.preset];
  const patchProfile = (k, v) => setProfiles(prev => ({ ...prev, [job.preset]: { ...prev[job.preset], [k]: v } }));
  const retry = id => setJobs(prev => prev.map(j => j.id === id ? { ...j, state: 'queued', pct: 0, speed: 0 } : j));
  const shown = filter === 'all' ? jobs : jobs.filter(j => j.state === filter);
  const workerOf = id => { const i = active.findIndex(j => j.id === id); return i < 0 ? null : WORKERS[i]; };

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', background: 'var(--bg-base)', color: 'var(--fg1)' }}>
      {/* ── top bar */}
      <div style={{ height: 56, flex: 'none', display: 'flex', alignItems: 'center', gap: 16, padding: '0 18px', background: 'var(--bg-panel)', borderBottom: '1px solid var(--line)' }}>
        <ConsoleCrumb trail={[{ label: 'Workspaces' }, { label: 'aether-media', mono: true }, { label: 'File transcoder' }]} />
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 11px', borderRadius: 'var(--r-xs)', border: `1px solid ${running ? 'var(--line-strong)' : 'var(--line)'}`, background: running ? 'var(--blue-tint)' : 'transparent' }}>
          <Dot tone={running ? 'ok' : 'idle'} pulse={running} />
          <span style={{ font: '600 11px var(--font-sans)', letterSpacing: '.14em', textTransform: 'uppercase', color: running ? '#fff' : 'var(--fg3)', whiteSpace: 'nowrap' }}>{running ? 'Queue running' : 'Queue paused'}</span>
        </div>
        <span style={{ font: '400 13px var(--font-mono)', color: '#fff', fontVariantNumeric: 'tabular-nums' }}>{active.length}/{MAX_CONCURRENT} · {queued.length} waiting</span>
        <div style={{ width: 1, height: 22, background: 'var(--line)' }} />
        <button className="ae-b" onClick={() => setRunning(r => !r)} style={{
          display: 'inline-flex', alignItems: 'center', gap: 8, minHeight: 38, padding: '10px 15px',
          borderRadius: 'var(--r-sm)', cursor: 'pointer', font: 'var(--t-btn)', letterSpacing: 'var(--ls-btn)',
          textTransform: 'uppercase', color: '#fff', whiteSpace: 'nowrap',
          background: running ? 'transparent' : 'var(--blue-500)',
          border: `1px solid ${running ? 'var(--line-strong)' : 'transparent'}`,
        }}>
          <Icon name={running ? 'pause' : 'play'} size={13} fill={running ? 'currentColor' : 'none'} />
          {running ? 'Pause queue' : 'Resume queue'}
        </button>
        <button className="ae-b" onClick={addMedia} style={{
          display: 'inline-flex', alignItems: 'center', gap: 8, minHeight: 38, padding: '10px 15px',
          borderRadius: 'var(--r-sm)', cursor: 'pointer', font: 'var(--t-btn)', letterSpacing: 'var(--ls-btn)',
          textTransform: 'uppercase', color: '#fff', background: 'var(--blue-500)', border: '1px solid transparent', whiteSpace: 'nowrap',
        }}>
          <Icon name="upload" size={13} />Add media
        </button>
        <div style={{ width: 1, height: 22, background: 'var(--line)' }} />
        <UserMenu />
      </div>

      {/* ── body */}
      <div style={{ flex: 1, display: 'flex', gap: 12, padding: 12, minHeight: 0 }}>
        {/* left: source inspector + farm */}
        <div style={{ width: 292, flex: 'none', display: 'flex', flexDirection: 'column', gap: 12, minHeight: 0 }}>
          <Panel title="Source file" style={{ flex: 1, minHeight: 0 }} bodyStyle={{ overflowY: 'auto', flex: 1 }}
            right={<span style={{ font: '400 10px var(--font-mono)', color: 'var(--fg3)' }}>{job.size.toFixed(1)} GB</span>}>
            <div style={{ position: 'relative', aspectRatio: '16 / 9', borderRadius: 'var(--r-xs)', overflow: 'hidden', border: '1px solid var(--line)', background: 'var(--bg-void)', marginBottom: 12 }}>
            <div style={{ position: 'absolute', inset: 0 }}>
              <image-slot id="ft-poster" shape="rect" fit="cover" placeholder="Drop a frame from this title"></image-slot>
            </div>
              <span style={{ position: 'absolute', left: 8, top: 8, pointerEvents: 'none', font: '600 9px var(--font-sans)', letterSpacing: '.14em', textTransform: 'uppercase', color: '#fff', background: 'rgba(3,5,6,.7)', padding: '4px 7px', borderRadius: 'var(--r-xs)' }}>Poster · 00:00:04</span>
              <span style={{ position: 'absolute', right: 8, bottom: 7, pointerEvents: 'none', font: '400 10px var(--font-mono)', color: '#fff', background: 'rgba(3,5,6,.7)', padding: '3px 6px', borderRadius: 'var(--r-xs)' }}>{fmtDur(job.dur)}</span>
            </div>
            <div style={{ font: '400 12px var(--font-mono)', color: '#fff', marginBottom: 12, wordBreak: 'break-all' }}>{job.file}</div>
            <Row label="Container" value={src.container} />
            <Row label="Codec in" value={src.codec} />
            <Row label="Resolution" value={`${src.w}×${src.h}p${src.fps}`} />
            <Row label="Chroma" value={src.chroma} />
            <Row label="Source rate" value={`${src.rate} Mb/s`} />
            <Row label="Duration" value={fmtDur(job.dur)} />
            <div style={{ height: 1, background: 'var(--line)', margin: '12px 0' }} />
            <Eb style={{ marginBottom: 9 }}>Streams</Eb>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {[['video', `V · ${src.codec}`, '1 track'], ['audio-lines', `A · ${src.audio > 2 ? 'discrete' : 'stereo'}`, `${src.audio} tracks`], ['captions', 'S · timed text', `${src.subs} tracks`]].map(([ic, label, n]) => (
                <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Icon name={ic} size={13} color="var(--fg3)" />
                  <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg2)', flex: 1 }}>{label}</span>
                  <span style={{ font: '400 11px var(--font-mono)', color: 'var(--fg3)' }}>{n}</span>
                </div>
              ))}
            </div>
          </Panel>

          <Panel title="Transcode farm" style={{ flex: 'none' }} right={<span style={{ font: '400 10px var(--font-mono)', color: 'var(--fg3)', whiteSpace: 'nowrap' }}>{active.length}/{WORKERS.length} claimed</span>}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {WORKERS.map((w, i) => {
                const claimed = running && i < active.length;
                const util = claimed ? 58 + i * 11 : 2;
                return (
                  <div key={w.id} style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 7 }}>
                      <Dot tone={claimed ? 'ok' : 'idle'} />
                      <span style={{ font: '500 10px var(--font-sans)', letterSpacing: '.1em', textTransform: 'uppercase', color: '#fff' }}>{w.name}</span>
                      <span style={{ font: 'var(--t-micro)', color: 'var(--fg4)', flex: 1, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{w.gpu} · {w.region}</span>
                      <span style={{ font: '400 11px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{util}%</span>
                    </div>
                    <Meter pct={util} color={claimed ? 'var(--viz-2)' : 'var(--idle)'} />
                  </div>
                );
              })}
            </div>
          </Panel>
        </div>

        {/* center: batch progress + queue + log */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 12, minWidth: 0 }}>
          <Panel title="Batch progress" style={{ flex: 'none' }} right={<span style={{ font: '400 10px var(--font-mono)', color: 'var(--fg3)' }}>{jobs.length} jobs · this batch</span>}>
            <div style={{ display: 'flex', gap: 26, marginBottom: 11 }}>
              <Read value={String(done.length)} unit={`/ ${jobs.length}`} label="Completed" size={22} />
              <Read value={String(active.length)} label="In flight" size={22} />
              <Read value={String(queued.length)} label="Queued" size={22} />
              <Read value={String(failed.length)} label="Failed" size={22} tone={failed.length ? 'var(--err)' : undefined} />
              <Read value={running ? Math.round(fps).toLocaleString() : '0'} unit="fps" label="Farm throughput" size={22} />
              <Read value={running ? active.reduce((n, j) => n + j.speed, 0).toFixed(1) : '0.0'} unit="×" label="Realtime" size={22} />
            </div>
            <Graph data={hist} max={Math.max(900, fps * 1.25)} live={running} />
          </Panel>

          <div style={{ flex: 1, minHeight: 0, display: 'flex', position: 'relative' }}
            onDragOver={e => { e.preventDefault(); setOver(true); }}
            onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget)) setOver(false); }}
            onDrop={onDrop}>
          <Panel title="Job queue" style={{ flex: 1, minWidth: 0, minHeight: 0 }} bodyStyle={{ padding: 0, flex: 1, overflowY: 'auto' }}
            right={<div style={{ display: 'flex', gap: 6 }}>
              {[['all', 'All'], ['running', 'Running'], ['queued', 'Queued'], ['done', 'Done'], ['failed', 'Failed']].map(([k, l]) => (
                <TChip key={k} active={filter === k} onClick={() => setFilter(k)}>{l}</TChip>
              ))}
            </div>}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 96px 70px 124px 52px 62px 72px', alignItems: 'center', gap: 10, padding: '0 13px', height: 32, borderBottom: '1px solid var(--line)', font: '500 9px var(--font-sans)', letterSpacing: '.13em', textTransform: 'uppercase', color: 'var(--fg4)', position: 'sticky', top: 0, background: 'var(--bg-panel)', zIndex: 2 }}>
              <span>Source</span><span>Profile</span><span>Duration</span><span>Progress</span><span>Speed</span><span>Eta</span><span>State</span>
            </div>
            {filter === 'all' && uploads.filter(u => u.state !== 'done').map(u => {
              const dn = u.chunks.filter(c => c === 2).length;
              const upct = (dn / u.chunks.length) * 100;
              return (
                <div key={u.id} onClick={() => { setBottom('xfer'); setXfer(u.id); }} style={{
                  display: 'grid', gridTemplateColumns: '1fr 96px 70px 124px 52px 62px 72px', alignItems: 'center', gap: 10,
                  padding: '0 13px', minHeight: 46, cursor: 'pointer', borderBottom: '1px solid var(--line)',
                  background: xfer === u.id ? 'var(--blue-tint)' : 'transparent',
                  boxShadow: xfer === u.id ? 'inset 2px 0 0 var(--blue-500)' : 'none',
                }}>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                    <span style={{ font: '400 12px var(--font-mono)', color: '#fff', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{u.file}</span>
                    <span style={{ font: 'var(--t-micro)', color: u.state === 'error' ? 'var(--err)' : 'var(--fg4)', whiteSpace: 'nowrap' }}>
                      {u.state === 'error' ? 'link lost · resuming from chunk map' : `uploading · ${dn}/${u.chunks.length} chunks · ${u.size.toFixed(1)} GB`}
                    </span>
                  </div>
                  <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg4)' }}>—</span>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg4)' }}>—</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <span style={{ font: '400 12px var(--font-mono)', color: '#fff', width: 34, fontVariantNumeric: 'tabular-nums' }}>{upct.toFixed(0)}%</span>
                    <div style={{ flex: 1 }}><Meter pct={upct} color={u.state === 'error' ? 'var(--err)' : u.state === 'verifying' ? 'var(--warn)' : 'var(--viz-2)'} /></div>
                  </div>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{u.rate ? u.rate.toFixed(0) : '—'}</span>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg4)' }}>—</span>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 7, font: 'var(--t-micro)', color: 'var(--fg2)' }}>
                    <Dot tone={u.state === 'error' ? 'err' : u.state === 'verifying' ? 'warn' : u.state === 'paused' ? 'idle' : 'ok'} pulse={u.state === 'uploading'} />
                    {u.state === 'verifying' ? 'Verifying' : u.state === 'paused' ? 'Paused' : u.state === 'error' ? 'Resuming' : 'Uploading'}
                  </span>
                </div>
              );
            })}
            {shown.map(j => {
              const [tone, label] = STATE[j.state];
              const eta = j.speed ? ((100 - j.pct) / 100) * j.dur / j.speed : 0;
              const w = workerOf(j.id);
              return (
                <div key={j.id} onClick={() => setSel(j.id)} style={{
                  display: 'grid', gridTemplateColumns: '1fr 96px 70px 124px 52px 62px 72px', alignItems: 'center', gap: 10,
                  padding: '0 13px', minHeight: 46, cursor: 'pointer', borderBottom: '1px solid var(--line)',
                  background: sel === j.id ? 'var(--blue-tint)' : 'transparent',
                  boxShadow: sel === j.id ? 'inset 2px 0 0 var(--blue-500)' : 'none',
                  opacity: j.state === 'done' ? 0.62 : 1,
                }}>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                    <span style={{ font: '400 12px var(--font-mono)', color: '#fff', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{j.file}</span>
                    <span style={{ font: 'var(--t-micro)', color: j.err ? 'var(--err)' : 'var(--fg4)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {j.err ? j.err : `${w ? w.name + ' · ' : ''}${SRC[j.src].container.split(' · ')[0]} · ${j.size.toFixed(1)} GB`}
                    </span>
                  </div>
                  <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{profiles[j.preset].name}</span>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg3)', fontVariantNumeric: 'tabular-nums' }}>{fmtDur(j.dur)}</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <span style={{ font: '400 12px var(--font-mono)', color: j.state === 'queued' ? 'var(--fg4)' : '#fff', width: 34, fontVariantNumeric: 'tabular-nums' }}>{j.state === 'queued' ? '—' : Math.round(j.pct) + '%'}</span>
                    <div style={{ flex: 1 }}><Meter pct={j.pct} color={j.state === 'failed' ? 'var(--err)' : j.state === 'done' ? 'var(--ok)' : j.state === 'running' ? 'var(--blue-500)' : 'var(--idle)'} /></div>
                  </div>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{j.speed ? j.speed.toFixed(1) + '×' : '—'}</span>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{j.state === 'running' && running ? fmtEta(eta) : '—'}</span>
                  {j.state === 'failed'
                    ? <button className="ae-b" onClick={e => { e.stopPropagation(); retry(j.id); }} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, font: '600 10px var(--font-sans)', letterSpacing: '.09em', textTransform: 'uppercase', padding: '5px 8px', borderRadius: 'var(--r-xs)', cursor: 'pointer', background: 'transparent', border: '1px solid var(--line-strong)', color: 'var(--fg1)' }}>
                        <Icon name="rotate-ccw" size={11} />Retry
                      </button>
                    : <span style={{ display: 'flex', alignItems: 'center', gap: 7, font: 'var(--t-micro)', color: 'var(--fg2)' }}>
                        <Dot tone={j.state === 'running' && !running ? 'idle' : tone} pulse={j.state === 'running' && running} />
                        {j.state === 'running' && !running ? 'Paused' : label}
                      </span>}
                </div>
              );
            })}
          </Panel>
          <DropVeil over={over} />
          </div>

          <Panel title={bottom === 'xfer' ? 'Transfer' : 'Job log'} style={{ height: bottom === 'xfer' ? 236 : 148, flex: 'none' }} bodyStyle={{ padding: 0, flex: 1, minHeight: 0 }}
            right={<div style={{ display: 'flex', gap: 6 }}>
              <TChip active={bottom === 'xfer'} onClick={() => setBottom('xfer')}>Transfer{uploads.filter(u => u.state !== 'done').length ? ` · ${uploads.filter(u => u.state !== 'done').length}` : ''}</TChip>
              <TChip active={bottom === 'log'} onClick={() => setBottom('log')}>Job log</TChip>
            </div>}>
            {bottom === 'xfer'
              ? <TransferPanel uploads={uploads} sel={xfer} onSel={setXfer} pause={pause} resume={resume} breakLink={breakLink} cancel={cancel} />
              : <div ref={logRef} style={{ height: '100%', overflowY: 'auto', padding: '8px 13px', display: 'flex', flexDirection: 'column', gap: 3 }}>
              {log.map(l => (
                <div key={l.id} style={{ display: 'flex', gap: 10, font: '400 11px var(--font-mono)', color: 'var(--fg2)', whiteSpace: 'nowrap' }}>
                  <span style={{ color: 'var(--fg4)' }}>{l.t}</span>
                  <span style={{ color: l.k === 'warn' ? 'var(--warn)' : 'var(--blue-400)', width: 30 }}>{l.k}</span>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{l.m}</span>
                </div>
              ))}
            </div>}
          </Panel>
        </div>

        {/* right: EVE + output profile + delivery */}
        <div style={{ width: 312, flex: 'none', display: 'flex', flexDirection: 'column', gap: 12, minHeight: 0 }}>
          <Panel title="EVE" style={{ flex: 'none' }} right={
            <button className="ae-b" onClick={() => setEve(v => !v)} aria-pressed={eve} style={{
              width: 34, height: 19, borderRadius: 999, cursor: 'pointer', position: 'relative', padding: 0,
              background: eve ? 'var(--blue-500)' : 'var(--bg-hover)', border: 'none', flex: 'none',
            }}>
              <span style={{ position: 'absolute', top: 2.5, left: eve ? 17.5 : 2.5, width: 14, height: 14, borderRadius: '50%', background: '#fff', transition: 'left var(--dur-fast) var(--ease-std)' }} />
            </button>
          }>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 10 }}>
              <span style={{ font: 'var(--t-label)', color: '#fff' }}>Encoding · Verified · Efficient</span>
              <span style={{ font: 'var(--t-micro)', color: eve ? 'var(--blue-400)' : 'var(--fg4)', marginLeft: 'auto', whiteSpace: 'nowrap' }}>{eve ? 'Automated' : 'Manual profile'}</span>
            </div>
            {eve ? (
              <React.Fragment>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 9, marginBottom: 14 }}>
                  {EVE_STEPS.map(([ic, label, note]) => (
                    <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
                      <Dot tone="ok" />
                      <span style={{ font: 'var(--t-body-sm)', color: '#fff', whiteSpace: 'nowrap' }}>{label}</span>
                      <span style={{ font: 'var(--t-micro)', color: 'var(--fg4)', flex: 1, textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{note}</span>
                    </div>
                  ))}
                </div>
                <Eb style={{ marginBottom: 10 }}>Delivery formats</Eb>
                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                  {EVE_FORMATS.map(f => (
                    <TChip key={f} active={formats.includes(f)} onClick={() => setFormats(p => p.includes(f) ? p.filter(x => x !== f) : [...p, f])}>{f}</TChip>
                  ))}
                </div>
                <div style={{ font: 'var(--t-micro)', color: 'var(--fg4)', marginTop: 9 }}>Everything else is decided per title. You pick what comes out.</div>
              </React.Fragment>
            ) : (
              <div style={{ font: 'var(--t-body-sm)', color: 'var(--fg3)', lineHeight: 1.5 }}>Off. Jobs use the output profile below, exactly as configured.</div>
            )}
          </Panel>

          <Panel title="Output profile" style={{ flex: 1, minHeight: 0, opacity: eve ? 0.72 : 1 }} bodyStyle={{ overflowY: 'auto', flex: 1 }}
            right={<span style={{ font: 'var(--t-label)', color: '#fff', whiteSpace: 'nowrap' }}>{eve ? 'EVE · per title' : p.name}</span>}>
            {eve ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '10px 11px', marginBottom: 14, borderRadius: 'var(--r-xs)', background: 'var(--blue-tint)', border: '1px solid var(--line)' }}>
                <Icon name="lock" size={14} color="var(--blue-400)" />
                <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg2)' }}>Codec, ladder and rate control set by EVE</span>
              </div>
            ) : (
            <React.Fragment>
            <Eb style={{ marginBottom: 11 }}>Container</Eb>
            <div style={{ display: 'flex', gap: 6, marginBottom: 16 }}>
              {['MP4', 'MOV', 'HLS', 'DASH', 'WebM'].map(c => <TChip key={c} active={p.container === c} onClick={() => patchProfile('container', c)}>{c}</TChip>)}
            </div>
            <Eb style={{ marginBottom: 11 }}>Rate control</Eb>
            <div style={{ display: 'flex', gap: 6, marginBottom: 16 }}>
              {['CRF', 'VBR', 'CBR'].map(m => <TChip key={m} active={p.rc === m} onClick={() => patchProfile('rc', m)}>{m}</TChip>)}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              {p.rc === 'CRF'
                ? <DragSlider label="Quality · CRF" value={p.crf} min={14} max={32} step={1} unit="" onChange={v => patchProfile('crf', v)} />
                : <DragSlider label="Target bitrate" value={p.target} min={0.5} max={40} step={0.5} unit="Mb/s" onChange={v => patchProfile('target', v)} />}
              <DragSlider label="GOP length" value={p.gop} min={1} max={8} step={1} unit="s" onChange={v => patchProfile('gop', v)} />
            </div>
            <div style={{ height: 1, background: 'var(--line)', margin: '16px 0 12px' }} />
            <Eb style={{ marginBottom: 11 }}>Encoder speed</Eb>
            <div style={{ display: 'flex', gap: 5, marginBottom: 6 }}>
              {['fast', 'medium', 'slow'].map(s => <TChip key={s} active={p.speed === s} onClick={() => patchProfile('speed', s)}>{s}</TChip>)}
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', font: 'var(--t-micro)', color: 'var(--fg4)', marginBottom: 16 }}>
              <span>Cheapest</span><span>Smallest file</span>
            </div>
            </React.Fragment>
            )}
            <div style={{ height: 1, background: 'var(--line)', margin: '0 0 8px' }} />
            <Row label="Codec" value={eve ? 'AV1 + H.264' : p.codec} />
            <Row label="Resolution" value={eve ? 'Per-title ladder' : `${p.w}×${p.h}`} />
            <Row label="Frame rate" value={eve ? 'Source cadence' : `${p.fps} fps`} />
            <Row label="Keyframe" value={eve ? 'Scene-aligned' : `${p.gop * p.fps} frames`} />
            <Row label="Audio" value={eve ? 'Loudness normalised' : p.audio} />
            <Row label="Formats out" value={eve ? (formats.join(' · ') || 'none') : p.container} />
            <Row label="Est. output" value={eve ? `≈ ${(job.dur * 5.6 / 8 / 1024).toFixed(2)} GB` : `${(job.dur * (p.rc === 'CRF' ? p.crf * 0.24 : p.target) / 8 / 1024).toFixed(2)} GB`} />
          </Panel>

          <Panel title="Delivery" style={{ flex: 'none' }} right={<Icon name="plus" size={14} color="var(--fg3)" />}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {[
                { name: 'Media library', proto: 'API', host: 'aether.cloud/media/aether-media', note: 'On complete', tone: 'ok' },
                { name: 'CDN package', proto: 'HLS', host: 'cdn.aether.live/vod/s04', note: 'Full ladder', tone: 'ok' },
                { name: 'Archive', proto: 'S3', host: 's3://aether-ar/masters', note: 'Mezzanine', tone: 'ok' },
                { name: 'Notify', proto: 'HOOK', host: 'hooks.aether.cloud/jobs', note: 'Per job', tone: 'warn' },
              ].map(d => (
                <div key={d.name} style={{ display: 'flex', flexDirection: 'column', gap: 5, paddingBottom: 10, borderBottom: '1px solid var(--line)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Dot tone={running ? d.tone : 'idle'} />
                    <span style={{ font: 'var(--t-label)', color: '#fff', flex: 1 }}>{d.name}</span>
                    <span style={{ font: '500 9px var(--font-sans)', letterSpacing: '.12em', color: 'var(--blue-400)' }}>{d.proto}</span>
                  </div>
                  <div style={{ display: 'flex', gap: 8, font: '400 10px var(--font-mono)', color: 'var(--fg4)' }}>
                    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.host}</span>
                    <span style={{ color: 'var(--fg3)' }}>{d.note}</span>
                  </div>
                </div>
              ))}
            </div>
          </Panel>
        </div>
      </div>
    </div>
  );
}

function mount() {
  ReactDOM.createRoot(document.getElementById('root')).render(
    <div style={{ position: 'fixed', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#08090B', overflow: 'hidden' }}>
      <div id="frame" style={{ position: 'relative', width: 1440, height: 900, flex: 'none', border: '1px solid var(--line)', borderRadius: 10, overflow: 'hidden', transformOrigin: 'center center' }}>
        <FileTranscoder />
      </div>
    </div>
  );
  const fit = () => {
    const f = document.getElementById('frame');
    if (!f) return;
    const s = Math.min((window.innerWidth - 24) / 1440, (window.innerHeight - 24) / 900, 1);
    f.style.transform = `scale(${s})`;
  };
  window.addEventListener('resize', fit);
  setInterval(fit, 400);
  fit();
}
window.mount = mount;
