// Transcoder.jsx — Aether Cloud · Transcoder console. Brand-side surface: navy
// grounds, one signal blue, mono numerals, no shadows, tracked uppercase labels.
// Live: ingest link bitrates, per-rendition output rates, GPU load, aggregate
// graph and the log stream all run off one 500ms tick. Sliders and toggles are real.

const LINKS = [
  { id: 'wan1', name: 'MODEM 1', carrier: 'Verizon LTE',  target: 7.4, rtt: 28 },
  { id: 'wan2', name: 'MODEM 2', carrier: 'AT&T 5G',      target: 9.1, rtt: 34 },
  { id: 'wan3', name: 'WIFI',    carrier: 'Venue AP',     target: 4.2, rtt: 61 },
  { id: 'wan4', name: 'ETH',     carrier: 'House uplink', target: 3.9, rtt: 11 },
];

const INITIAL_LADDER = [
  { id: 'r1', name: '2160p60', w: 3840, h: 2160, fps: 60, codec: 'HEVC',  profile: 'Main 10', target: 18.0, on: true,  rc: 'CBR', gop: 2, bframes: 2, preset: 'p5' },
  { id: 'r2', name: '1080p60', w: 1920, h: 1080, fps: 60, codec: 'H.264', profile: 'High',    target: 8.0,  on: true,  rc: 'CBR', gop: 2, bframes: 2, preset: 'p5' },
  { id: 'r3', name: '720p60',  w: 1280, h: 720,  fps: 60, codec: 'H.264', profile: 'High',    target: 4.5,  on: true,  rc: 'CBR', gop: 2, bframes: 1, preset: 'p4' },
  { id: 'r4', name: '540p30',  w: 960,  h: 540,  fps: 30, codec: 'H.264', profile: 'Main',    target: 2.2,  on: true,  rc: 'VBR', gop: 4, bframes: 1, preset: 'p4' },
  { id: 'r5', name: '360p30',  w: 640,  h: 360,  fps: 30, codec: 'H.264', profile: 'Baseline', target: 0.9, on: false, rc: 'VBR', gop: 4, bframes: 0, preset: 'p3' },
];

const DESTS = [
  { id: 'd1', name: 'YouTube Live',   proto: 'RTMPS', host: 'a.rtmps.youtube.com/live2', rend: '2160p60', tone: 'ok' },
  { id: 'd2', name: 'Studio Return',  proto: 'SRT',   host: 'srt://10.4.2.18:9001',      rend: '1080p60', tone: 'ok' },
  { id: 'd3', name: 'CDN Package',    proto: 'HLS',   host: 'cdn.aether.live/pkg/0121',  rend: 'Full ladder', tone: 'ok' },
  { id: 'd4', name: 'Archive',        proto: 'S3',    host: 's3://aether-ar/0121',       rend: '2160p60', tone: 'warn' },
];

const LOG_POOL = [
  ['srt', 'link MODEM 2 rtt 34 ms · loss 0.42% · recovered'],
  ['enc', 'IDR keyframe 2160p60 gop 120'],
  ['abr', 'ladder stable · 5 renditions · 33.6 Mb/s aggregate'],
  ['out', 'rtmps ack 18.02 Mb/s · buffer 1.1 s'],
  ['dec', 'NVDEC hevc_10bit 3840x2160p60 · 0 dropped'],
  ['gpu', 'nvenc session 5/8 · sm 61% · vram 3.2/24 GB'],
  ['srt', 'link WIFI rtt 61 ms · reordering window 42 ms'],
  ['pkg', 'hls segment 0942.ts written · 4.00 s'],
  ['out', 'srt caller 10.4.2.18 ack · rtt 4 ms'],
  ['sys', 'power draw 71 W · junction 64 °C'],
];

const { stamp, jit, mb, Eb, Panel, Dot, Read, Meter, TChip, DragSlider, Row, Graph, ConsoleCrumb, UserMenu } = window;

function Transcoder() {
  const [ladder, setLadder] = React.useState(INITIAL_LADDER);
  const [sel, setSel] = React.useState('r2');
  const [live, setLive] = React.useState(true);
  const [tick, setTick] = React.useState(0);
  const [frames, setFrames] = React.useState(45710);
  const [hist, setHist] = React.useState(() => Array.from({ length: 60 }, () => 30 + Math.random() * 4));
  const [log, setLog] = React.useState(() => LOG_POOL.slice(0, 6).map((l, i) => ({ id: i, t: stamp(), k: l[0], m: l[1] })));
  const logRef = React.useRef(null);

  const enabled = ladder.filter(r => r.on);
  const aggregate = enabled.reduce((n, r) => n + r.target, 0) + 0.128;
  const shareOf = x => (x.w * x.h * x.fps) / 1.08e7;
  const gpu = Math.min(97, 8 + enabled.reduce((n, x) => n + shareOf(x), 0));

  React.useEffect(() => {
    const iv = setInterval(() => {
      setTick(t => t + 1);
      if (!live) return;
      setFrames(f => f + 30);
      setHist(h => [...h.slice(1), jit(aggregate, aggregate * 0.06)]);
      if (Math.random() < 0.55) {
        const l = LOG_POOL[Math.floor(Math.random() * LOG_POOL.length)];
        setLog(p => [...p.slice(-40), { id: Math.random(), t: stamp(), k: l[0], m: l[1] }]);
      }
    }, 500);
    return () => clearInterval(iv);
  }, [live, aggregate]);

  React.useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [log]);

  const secs = 761 + Math.floor(tick / 2);
  const tc = `${String(Math.floor(secs / 3600)).padStart(2, '0')}:${String(Math.floor(secs / 60) % 60).padStart(2, '0')}:${String(secs % 60).padStart(2, '0')}:${String((tick * 7) % 60).padStart(2, '0')}`;
  const inRate = live ? jit(24.6, 0.5) : 0;
  const r = ladder.find(x => x.id === sel);
  const patch = (id, k, v) => setLadder(p => p.map(x => x.id === id ? { ...x, [k]: v } : x));

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', background: 'var(--bg-base)', color: 'var(--fg1)' }}>
      {/* ── top bar */}
      <div style={{ height: 56, flex: 'none', display: 'flex', alignItems: 'center', gap: 16, padding: '0 18px', background: 'var(--bg-panel)', borderBottom: '1px solid var(--line)' }}>
        <ConsoleCrumb trail={[{ label: 'Fleet' }, { label: 'AET-0121', mono: true }, { label: 'Transcoder' }]} />
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 11px', borderRadius: 'var(--r-xs)', border: `1px solid ${live ? 'rgba(229,51,78,.4)' : 'var(--line)'}`, background: live ? 'rgba(229,51,78,.1)' : 'transparent' }}>
          <Dot tone={live ? 'onair' : 'idle'} pulse={live} />
          <span style={{ font: '600 11px var(--font-sans)', letterSpacing: '.14em', textTransform: 'uppercase', color: live ? '#fff' : 'var(--fg3)', whiteSpace: 'nowrap' }}>{live ? 'On air' : 'Stopped'}</span>
        </div>
        <span style={{ font: '400 13px var(--font-mono)', color: '#fff', fontVariantNumeric: 'tabular-nums' }}>{tc}</span>
        <div style={{ width: 1, height: 22, background: 'var(--line)' }} />
        <button className="ae-b" onClick={() => setLive(l => !l)} style={{
          display: 'inline-flex', alignItems: 'center', gap: 8, minHeight: 38, padding: '10px 15px',
          borderRadius: 'var(--r-sm)', cursor: 'pointer', font: 'var(--t-btn)', letterSpacing: 'var(--ls-btn)',
          textTransform: 'uppercase', color: '#fff', whiteSpace: 'nowrap',
          background: live ? 'transparent' : 'var(--blue-500)',
          border: `1px solid ${live ? 'var(--line-strong)' : 'transparent'}`,
        }}>
          <Icon name={live ? 'square' : 'play'} size={13} fill={live ? 'currentColor' : 'none'} />
          {live ? 'Stop transcode' : 'Start transcode'}
        </button>
        <div style={{ width: 1, height: 22, background: 'var(--line)' }} />
        <UserMenu />
      </div>

      {/* ── body */}
      <div style={{ flex: 1, display: 'flex', gap: 12, padding: 12, minHeight: 0 }}>
        {/* left: ingest + hardware */}
        <div style={{ width: 292, flex: 'none', display: 'flex', flexDirection: 'column', gap: 12, minHeight: 0 }}>
          <Panel title="Ingest" right={<span style={{ font: '400 10px var(--font-mono)', color: 'var(--fg3)' }}>SRT :2059</span>}>
            <Eb style={{ marginBottom: 9 }}>Bonded source</Eb>
            <div style={{ display: 'flex', gap: 16, marginBottom: 14 }}>
              <Read value={live ? mb(inRate) : '0.00'} unit="Mb/s" label="Input rate" />
              <Read value={live ? '31' : '—'} unit="ms" label="Link rtt" />
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
              {LINKS.map(l => {
                const v = live ? jit(l.target, 0.7) : 0;
                return (
                  <div key={l.id} style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 7 }}>
                      <Dot tone={live ? 'ok' : 'idle'} />
                      <span style={{ font: '500 10px var(--font-sans)', letterSpacing: '.1em', textTransform: 'uppercase', color: '#fff' }}>{l.name}</span>
                      <span style={{ font: 'var(--t-micro)', color: 'var(--fg4)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{l.carrier}</span>
                      <span style={{ font: '400 11px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{v.toFixed(1)}</span>
                    </div>
                    <Meter pct={(v / 10) * 100} color={live ? 'var(--viz-2)' : 'var(--idle)'} />
                  </div>
                );
              })}
            </div>
            <div style={{ height: 1, background: 'var(--line)', margin: '14px 0 12px' }} />
            <Row label="Source" value="3840×2160p60" />
            <Row label="Codec in" value="HEVC 10-bit" />
            <Row label="Decode" value="NVDEC · hw" />
            <Row label="Loss recovered" value={live ? '0.42 %' : '—'} />
          </Panel>

          <Panel title="Encoder hardware" style={{ flex: 'none' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 11 }}>
              {[
                ['GPU · NVENC', live ? gpu : 0, '%', 'var(--blue-500)'],
                ['CPU', live ? 26 : 3, '%', 'var(--viz-3)'],
                ['VRAM', live ? 13 : 2, '%', 'var(--viz-4)'],
              ].map(([label, v, u, c]) => (
                <div key={label} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ font: '500 10px var(--font-sans)', letterSpacing: '.13em', textTransform: 'uppercase', color: 'var(--fg3)' }}>{label}</span>
                    <span style={{ font: '400 11px var(--font-mono)', color: '#fff', fontVariantNumeric: 'tabular-nums' }}>{v.toFixed(0)}{u}</span>
                  </div>
                  <Meter pct={v} color={c} />
                </div>
              ))}
              <div style={{ display: 'flex', gap: 18, paddingTop: 4 }}>
                <Read value={live ? '64' : '38'} unit="°C" label="Junction" size={16} />
                <Read value={live ? '71' : '12'} unit="W" label="Draw" size={16} />
                <Read value={`${enabled.length}/8`} label="Sessions" size={16} />
              </div>
            </div>
          </Panel>
        </div>

        {/* center: monitor + ladder + graph */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 12, minWidth: 0 }}>
          <div style={{ display: 'flex', gap: 12, flex: 'none' }}>
            {/* program monitor */}
            <div style={{ width: 268, flex: 'none', position: 'relative', aspectRatio: '16 / 9', borderRadius: 'var(--r-md)', overflow: 'hidden', border: `1px solid ${live ? 'rgba(229,51,78,.45)' : 'var(--line)'}`, background: 'var(--bg-void)' }}>
              <div style={{ position: 'absolute', inset: 0, background: 'linear-gradient(140deg,#1B2440,#080B10 70%)' }} />
              <div style={{ position: 'absolute', left: '12%', top: '18%', width: '46%', height: '54%', borderRadius: '50%', background: 'radial-gradient(circle,rgba(47,107,246,.5),transparent 68%)' }} />
              <div style={{ position: 'absolute', right: '10%', bottom: '12%', width: '38%', height: '40%', background: 'linear-gradient(150deg,#2C3A52,#141A24)' }} />
              {!live && <div style={{ position: 'absolute', inset: 0, background: 'rgba(3,5,6,.72)', display: 'flex', alignItems: 'center', justifyContent: 'center', font: '500 11px var(--font-sans)', letterSpacing: '.14em', textTransform: 'uppercase', color: 'var(--fg3)' }}>No program</div>}
              <div style={{ position: 'absolute', left: 9, top: 9, display: 'flex', alignItems: 'center', gap: 6, padding: '4px 8px', background: 'rgba(3,5,6,.7)', borderRadius: 'var(--r-xs)' }}>
                <Dot tone={live ? 'onair' : 'idle'} pulse={live} />
                <span style={{ font: '600 9px var(--font-sans)', letterSpacing: '.14em', textTransform: 'uppercase', color: '#fff' }}>Program</span>
              </div>
              <span style={{ position: 'absolute', right: 9, bottom: 8, font: '400 10px var(--font-mono)', color: '#fff', background: 'rgba(3,5,6,.7)', padding: '3px 6px', borderRadius: 'var(--r-xs)' }}>{tc}</span>
            </div>

            {/* aggregate */}
            <Panel title="Aggregate output" style={{ flex: 1, minWidth: 0 }} right={<span style={{ font: '400 10px var(--font-mono)', color: 'var(--fg3)' }}>{enabled.length + 1} streams</span>}>
              <div style={{ display: 'flex', gap: 22, marginBottom: 11 }}>
                <Read value={live ? mb(hist[hist.length - 1]) : '0.00'} unit="Mb/s" label="Egress" size={22} />
                <Read value={live ? '412' : '—'} unit="ms" label="Glass to glass" size={22} />
                <Read value={frames.toLocaleString()} label="Frames encoded" size={22} />
                <Read value={live ? '0' : '—'} label="Dropped" size={22} tone={live ? 'var(--ok)' : undefined} />
              </div>
              <Graph data={live ? hist : hist.map(() => 0)} max={Math.max(44, aggregate * 1.3)} live={live} />
            </Panel>
          </div>

          {/* ladder */}
          <Panel title="Transcode ladder" bodyStyle={{ padding: 0, flex: 1, overflowY: 'auto' }} style={{ flex: 1, minHeight: 0 }}
            right={<div style={{ display: 'flex', gap: 6 }}><TChip active>ABR ladder</TChip><TChip>Passthrough</TChip></div>}>
            <div style={{ display: 'grid', gridTemplateColumns: '38px 96px 78px 88px 92px 1fr 74px 70px', alignItems: 'center', padding: '0 13px', height: 32, borderBottom: '1px solid var(--line)', font: '500 9px var(--font-sans)', letterSpacing: '.13em', textTransform: 'uppercase', color: 'var(--fg4)', position: 'sticky', top: 0, background: 'var(--bg-panel)', zIndex: 2 }}>
              <span>On</span><span>Rendition</span><span>Codec</span><span>Profile</span><span>Target</span><span>Measured</span><span>GPU</span><span>State</span>
            </div>
            {ladder.map(x => {
              const on = x.on && live;
              const meas = on ? jit(x.target, x.target * 0.05) : 0;
              const share = on ? shareOf(x) : 0;
              return (
                <div key={x.id} onClick={() => setSel(x.id)} style={{
                  display: 'grid', gridTemplateColumns: '38px 96px 78px 88px 92px 1fr 74px 70px', alignItems: 'center',
                  padding: '0 13px', height: 44, cursor: 'pointer', borderBottom: '1px solid var(--line)',
                  background: sel === x.id ? 'var(--blue-tint)' : 'transparent',
                  boxShadow: sel === x.id ? 'inset 2px 0 0 var(--blue-500)' : 'none',
                  opacity: x.on ? 1 : 0.45,
                }}>
                  <button className="ae-b" onClick={e => { e.stopPropagation(); patch(x.id, 'on', !x.on); }} style={{
                    width: 26, height: 15, borderRadius: 999, cursor: 'pointer', position: 'relative', padding: 0,
                    background: x.on ? 'var(--blue-500)' : 'var(--bg-hover)', border: 'none',
                  }}>
                    <span style={{ position: 'absolute', top: 2, left: x.on ? 13 : 2, width: 11, height: 11, borderRadius: '50%', background: '#fff', transition: 'left var(--dur-fast) var(--ease-std)' }} />
                  </button>
                  <span style={{ font: '500 13px var(--font-mono)', color: '#fff' }}>{x.name}</span>
                  <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg2)' }}>{x.codec}</span>
                  <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg3)' }}>{x.profile}</span>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{x.target.toFixed(1)} Mb/s</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, paddingRight: 16 }}>
                    <span style={{ font: '400 12px var(--font-mono)', color: on ? '#fff' : 'var(--fg4)', width: 46, fontVariantNumeric: 'tabular-nums' }}>{on ? meas.toFixed(2) : '—'}</span>
                    <div style={{ flex: 1 }}><Meter pct={on ? (meas / x.target) * 100 : 0} color={on ? 'var(--blue-500)' : 'var(--idle)'} /></div>
                  </div>
                  <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}>{on ? share.toFixed(0) + '%' : '—'}</span>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 7, font: 'var(--t-micro)', color: 'var(--fg2)' }}>
                    <Dot tone={!x.on ? 'idle' : live ? 'ok' : 'idle'} />{!x.on ? 'Off' : live ? 'Encoding' : 'Idle'}
                  </span>
                </div>
              );
            })}
            <div style={{ display: 'grid', gridTemplateColumns: '38px 96px 78px 1fr 70px', alignItems: 'center', padding: '0 13px', height: 44, borderBottom: '1px solid var(--line)' }}>
              <Icon name="audio-lines" size={14} color="var(--viz-3)" />
              <span style={{ font: '500 13px var(--font-mono)', color: '#fff' }}>Audio</span>
              <span style={{ font: 'var(--t-body-sm)', color: 'var(--fg2)' }}>AAC-LC</span>
              <span style={{ font: '400 12px var(--font-mono)', color: 'var(--fg3)' }}>128 kb/s · 48 kHz · stereo</span>
              <span style={{ display: 'flex', alignItems: 'center', gap: 7, font: 'var(--t-micro)', color: 'var(--fg2)' }}>
                <Dot tone={live ? 'ok' : 'idle'} />{live ? 'Encoding' : 'Idle'}
              </span>
            </div>
          </Panel>

          {/* log */}
          <Panel title="Pipeline log" style={{ height: 148, flex: 'none' }} bodyStyle={{ padding: 0, flex: 1, minHeight: 0 }}
            right={<div style={{ display: 'flex', gap: 6 }}><TChip active>All</TChip><TChip>Warnings</TChip><TChip>Errors</TChip></div>}>
            <div ref={logRef} style={{ height: '100%', overflowY: 'auto', padding: '8px 13px', display: 'flex', flexDirection: 'column', gap: 3 }}>
              {log.map((l, i) => (
                <div key={l.id} style={{ display: 'flex', gap: 10, font: '400 11px var(--font-mono)', color: 'var(--fg2)', whiteSpace: 'nowrap' }}>
                  <span style={{ color: 'var(--fg4)' }}>{l.t}</span>
                  <span style={{ color: 'var(--blue-400)', width: 30 }}>{l.k}</span>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{l.m}</span>
                </div>
              ))}
            </div>
          </Panel>
        </div>

        {/* right: rendition params + destinations */}
        <div style={{ width: 312, flex: 'none', display: 'flex', flexDirection: 'column', gap: 12, minHeight: 0 }}>
          <Panel title="Rendition" style={{ flex: 1, minHeight: 0 }} bodyStyle={{ overflowY: 'auto', flex: 1 }}
            right={<span style={{ font: '500 13px var(--font-mono)', color: '#fff' }}>{r.name}</span>}>
            <Eb style={{ marginBottom: 11 }}>Rate control</Eb>
            <div style={{ display: 'flex', gap: 6, marginBottom: 16 }}>
              {['CBR', 'VBR', 'CQ'].map(m => <TChip key={m} active={r.rc === m} onClick={() => patch(r.id, 'rc', m)}>{m}</TChip>)}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <DragSlider label="Target bitrate" value={r.target} min={0.4} max={24} step={0.1} unit="Mb/s" onChange={v => patch(r.id, 'target', v)} />
              <DragSlider label="GOP length" value={r.gop} min={1} max={8} step={1} unit="s" onChange={v => patch(r.id, 'gop', v)} />
              <DragSlider label="B-frames" value={r.bframes} min={0} max={4} step={1} unit="" onChange={v => patch(r.id, 'bframes', v)} />
            </div>
            <div style={{ height: 1, background: 'var(--line)', margin: '16px 0 12px' }} />
            <Eb style={{ marginBottom: 11 }}>Quality preset</Eb>
            <div style={{ display: 'flex', gap: 5, marginBottom: 6 }}>
              {['p1', 'p3', 'p4', 'p5', 'p7'].map(p => <TChip key={p} active={r.preset === p} onClick={() => patch(r.id, 'preset', p)}>{p}</TChip>)}
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', font: 'var(--t-micro)', color: 'var(--fg4)', marginBottom: 16 }}>
              <span>Fastest</span><span>Highest quality</span>
            </div>
            <div style={{ height: 1, background: 'var(--line)', margin: '0 0 8px' }} />
            <Row label="Resolution" value={`${r.w}×${r.h}`} />
            <Row label="Frame rate" value={`${r.fps} fps`} />
            <Row label="Keyframe" value={`${r.gop * r.fps} frames`} />
            <Row label="Profile" value={r.profile} />
            <Row label="Chroma" value="4:2:0 · 8-bit" />
            <Row label="Latency mode" value="Low · ultra" />
          </Panel>

          <Panel title="Destinations" style={{ flex: 'none' }} right={<Icon name="plus" size={14} color="var(--fg3)" />}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {DESTS.map(d => (
                <div key={d.id} style={{ display: 'flex', flexDirection: 'column', gap: 5, paddingBottom: 10, borderBottom: '1px solid var(--line)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Dot tone={live ? d.tone : 'idle'} />
                    <span style={{ font: 'var(--t-label)', color: '#fff', flex: 1 }}>{d.name}</span>
                    <span style={{ font: '500 9px var(--font-sans)', letterSpacing: '.12em', color: 'var(--blue-400)' }}>{d.proto}</span>
                  </div>
                  <div style={{ display: 'flex', gap: 8, font: '400 10px var(--font-mono)', color: 'var(--fg4)' }}>
                    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.host}</span>
                    <span style={{ color: 'var(--fg3)' }}>{d.rend}</span>
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
        <Transcoder />
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
