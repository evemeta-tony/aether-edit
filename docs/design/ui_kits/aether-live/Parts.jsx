// Parts.jsx — shared console primitives for the Aether Cloud surfaces.
// Navy grounds, one signal blue, mono numerals, tracked uppercase labels, no shadows.

const stamp = () => {
  const d = new Date();
  return `${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}:${String(d.getSeconds()).padStart(2,'0')}`;
};
const jit = (v, amt) => Math.max(0, v + (Math.random() - 0.5) * amt);
const mb = v => v.toFixed(2);

function Eb({ children, style }) {
  return <div style={{ font: 'var(--t-eyebrow)', letterSpacing: 'var(--ls-eyebrow)', textTransform: 'uppercase', color: 'var(--blue-400)', ...style }}>{children}</div>;
}

function Panel({ title, right, children, style, bodyStyle }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', background: 'var(--bg-panel)', border: '1px solid var(--line)', borderRadius: 'var(--r-md)', minHeight: 0, ...style }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 13px', borderBottom: '1px solid var(--line)', flex: 'none' }}>
        <span style={{ font: 'var(--t-eyebrow)', letterSpacing: 'var(--ls-eyebrow)', textTransform: 'uppercase', color: 'var(--fg3)' }}>{title}</span>
        <div style={{ flex: 1 }} />
        {right}
      </div>
      <div style={{ padding: 13, minHeight: 0, ...bodyStyle }}>{children}</div>
    </div>
  );
}

function Dot({ tone = 'ok', pulse }) {
  const c = { ok: 'var(--ok)', warn: 'var(--warn)', err: 'var(--err)', idle: 'var(--idle)', onair: 'var(--onair)' }[tone];
  return <span style={{
    width: 7, height: 7, borderRadius: '50%', background: c, flex: 'none',
    boxShadow: tone === 'onair' ? '0 0 0 3px rgba(229,51,78,.22)' : 'none',
    animation: pulse ? 'ae-pulse var(--dur-glow) var(--ease-std) infinite' : 'none',
  }} />;
}

// Numeral readout: mono value + tracked unit caption. The house spec pattern.
function Read({ value, unit, label, tone, size = 19 }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 4 }}>
        <span style={{ font: `500 ${size}px var(--font-mono)`, color: tone || '#fff', letterSpacing: '-.01em', fontVariantNumeric: 'tabular-nums' }}>{value}</span>
        {unit && <span style={{ font: 'var(--t-micro)', color: 'var(--fg3)' }}>{unit}</span>}
      </div>
      <span style={{ font: '500 9px var(--font-sans)', letterSpacing: '.13em', textTransform: 'uppercase', color: 'var(--fg4)', whiteSpace: 'nowrap' }}>{label}</span>
    </div>
  );
}

function Meter({ pct, color = 'var(--blue-500)', h = 3 }) {
  return (
    <div style={{ height: h, background: 'var(--bg-input)', borderRadius: 1, overflow: 'hidden' }}>
      <div style={{ height: '100%', width: Math.min(100, pct) + '%', background: color, transition: 'width .45s linear' }} />
    </div>
  );
}

function TChip({ children, active, onClick }) {
  return (
    <button className="ae-b" onClick={onClick} style={{
      font: '600 10px var(--font-sans)', letterSpacing: '.09em', textTransform: 'uppercase',
      padding: '6px 10px', borderRadius: 'var(--r-xs)', cursor: 'pointer', whiteSpace: 'nowrap',
      background: active ? 'var(--blue-tint)' : 'transparent',
      border: `1px solid ${active ? 'var(--blue-500)' : 'var(--line-strong)'}`,
      color: active ? 'var(--blue-300)' : 'var(--fg2)',
    }}>{children}</button>
  );
}

// Real drag slider.
function DragSlider({ label, value, min, max, step = 0.1, unit, onChange }) {
  const ref = React.useRef(null);
  const pct = ((value - min) / (max - min)) * 100;
  const grab = e => {
    const el = ref.current;
    const set = ev => {
      const r = el.getBoundingClientRect();
      const t = Math.max(0, Math.min(1, (ev.clientX - r.left) / r.width));
      onChange(Math.round((min + t * (max - min)) / step) * step);
    };
    set(e);
    const up = () => { window.removeEventListener('pointermove', set); window.removeEventListener('pointerup', up); };
    window.addEventListener('pointermove', set); window.addEventListener('pointerup', up);
  };
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <span style={{ font: '500 10px var(--font-sans)', letterSpacing: '.13em', textTransform: 'uppercase', color: 'var(--fg3)' }}>{label}</span>
        <span style={{ font: '400 12px var(--font-mono)', color: '#fff', fontVariantNumeric: 'tabular-nums' }}>{value.toFixed(step < 1 ? 1 : 0)}<span style={{ color: 'var(--fg3)' }}> {unit}</span></span>
      </div>
      <div ref={ref} onPointerDown={grab} style={{ height: 22, display: 'flex', alignItems: 'center', cursor: 'ew-resize', touchAction: 'none' }}>
        <div style={{ position: 'relative', width: '100%', height: 2, background: 'var(--bg-hover)' }}>
          <div style={{ position: 'absolute', left: 0, top: 0, bottom: 0, width: pct + '%', background: 'var(--blue-500)' }} />
          <div style={{ position: 'absolute', left: `calc(${pct}% - 5px)`, top: -4, width: 10, height: 10, background: '#fff', borderRadius: 1 }} />
        </div>
      </div>
    </div>
  );
}

function Row({ label, value, tone }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 10, minHeight: 26 }}>
      <span style={{ font: '500 10px var(--font-sans)', letterSpacing: '.13em', textTransform: 'uppercase', color: 'var(--fg3)' }}>{label}</span>
      <span style={{ font: '400 12px var(--font-mono)', color: tone || '#fff', fontVariantNumeric: 'tabular-nums', textAlign: 'right' }}>{value}</span>
    </div>
  );
}

function Graph({ data, max, live }) {
  const W = 100, H = 100;
  const pts = data.map((v, i) => `${(i / (data.length - 1)) * W},${H - (v / max) * H}`).join(' ');
  return (
    <div style={{ position: 'relative', height: 76, background: 'var(--bg-void)', border: '1px solid var(--line)', borderRadius: 'var(--r-xs)', overflow: 'hidden' }}>
      {[25, 50, 75].map(p => <div key={p} style={{ position: 'absolute', left: 0, right: 0, top: p + '%', height: 1, background: 'rgba(255,255,255,.05)' }} />)}
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" style={{ position: 'absolute', inset: 0, width: '100%', height: '100%' }}>
        <polygon points={`0,100 ${pts} 100,100`} fill="rgba(47,107,246,.16)" />
        <polyline points={pts} fill="none" stroke={live ? 'var(--blue-400)' : 'var(--idle)'} strokeWidth="1" vectorEffect="non-scaling-stroke" />
      </svg>
      <span style={{ position: 'absolute', right: 7, top: 5, font: '400 9px var(--font-mono)', color: 'var(--fg4)' }}>{max.toFixed(0)} Mb/s</span>
      <span style={{ position: 'absolute', left: 7, bottom: 4, font: '400 9px var(--font-mono)', color: 'var(--fg4)' }}>−60 s</span>
    </div>
  );
}

// Wordmark + breadcrumb head shared by both consoles.
function ConsoleCrumb({ trail }) {
  return (
    <React.Fragment>
      <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
        <Mark size={19} />
        <span style={{ font: '600 12px var(--font-sans)', letterSpacing: 'var(--ls-wordmark)', color: '#fff' }}>AETHER</span>
        <span style={{ font: '500 11px var(--font-sans)', letterSpacing: '.14em', textTransform: 'uppercase', color: 'var(--fg3)' }}>Cloud</span>
      </div>
      <div style={{ width: 1, height: 22, background: 'var(--line)' }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, font: 'var(--t-body-sm)', color: 'var(--fg3)', whiteSpace: 'nowrap', flex: 'none' }}>
        {trail.map((t, i) => (
          <React.Fragment key={i}>
            {i > 0 && <Icon name="chevron-right" size={12} />}
            <span style={{ whiteSpace: 'nowrap', ...(i === trail.length - 1 ? { color: '#fff', font: 'var(--t-label)' } : t.mono ? { fontFamily: 'var(--font-mono)', color: 'var(--fg2)' } : {}) }}>{t.label}</span>
          </React.Fragment>
        ))}
      </div>
    </React.Fragment>
  );
}


// Account menu shared by the cloud consoles.
function UserMenu({ name = 'Marthe Reyes', email = 'marthe@aether-media.tv', org = 'aether-media', role = 'Media ops' }) {
  const [open, setOpen] = React.useState(false);
  return (
    <div style={{ position: 'relative' }}>
      <button className="ae-b" onClick={() => setOpen(m => !m)} style={{
        display: 'flex', alignItems: 'center', gap: 9, padding: '5px 9px 5px 5px', borderRadius: 'var(--r-sm)',
        cursor: 'pointer', background: open ? 'var(--bg-hover)' : 'transparent', border: '1px solid var(--line)',
      }}>
        <span style={{ width: 26, height: 26, borderRadius: '50%', flex: 'none', display: 'grid', placeItems: 'center', background: 'var(--blue-500)', font: '600 10px var(--font-sans)', letterSpacing: '.04em', color: '#fff' }}>{name.split(' ').map(s => s[0]).join('')}</span>
        <span style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 1 }}>
          <span style={{ font: 'var(--t-label)', color: '#fff', whiteSpace: 'nowrap' }}>{name}</span>
          <span style={{ font: 'var(--t-micro)', color: 'var(--fg4)', whiteSpace: 'nowrap' }}>{org} · {role}</span>
        </span>
        <Icon name="chevron-down" size={13} color="var(--fg3)" />
      </button>
      {open && (
        <div style={{ position: 'absolute', right: 0, top: 46, width: 262, zIndex: 30, background: 'var(--bg-panel)', border: '1px solid var(--line-strong)', borderRadius: 'var(--r-md)', overflow: 'hidden' }}>
          <div style={{ padding: '13px 14px', borderBottom: '1px solid var(--line)' }}>
            <div style={{ font: 'var(--t-label)', color: '#fff', marginBottom: 3 }}>{name}</div>
            <div style={{ font: '400 11px var(--font-mono)', color: 'var(--fg3)' }}>{email}</div>
          </div>
          <div style={{ padding: '11px 14px', borderBottom: '1px solid var(--line)', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <Eb>Workspace</Eb>
            {[[org, true], ['nordlys-studios', false], ['sandbox', false]].map(([w, on]) => (
              <div key={w} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Dot tone={on ? 'ok' : 'idle'} />
                <span style={{ font: 'var(--t-body-sm)', color: on ? '#fff' : 'var(--fg2)', flex: 1 }}>{w}</span>
                {on && <Icon name="check" size={13} color="var(--blue-400)" />}
              </div>
            ))}
          </div>
          <div style={{ padding: '11px 14px', borderBottom: '1px solid var(--line)' }}>
            <Row label="Plan" value="Scale · annual" />
            <Row label="Encode hours" value="812 / 1,500" />
            <div style={{ marginTop: 6 }}><Meter pct={54} /></div>
          </div>
          <div style={{ padding: 6, display: 'flex', flexDirection: 'column' }}>
            {[['user', 'Account settings'], ['key-round', 'API keys'], ['receipt', 'Billing & usage'], ['log-out', 'Sign out']].map(([ic, l]) => (
              <button key={l} className="ae-b" onClick={() => setOpen(false)} style={{
                display: 'flex', alignItems: 'center', gap: 10, padding: '9px 8px', borderRadius: 'var(--r-xs)',
                background: 'transparent', border: 'none', cursor: 'pointer', font: 'var(--t-body-sm)', color: 'var(--fg1)', textAlign: 'left',
              }}><Icon name={ic} size={14} color="var(--fg3)" />{l}</button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

Object.assign(window, { stamp, jit, mb, Eb, Panel, Dot, Read, Meter, TChip, DragSlider, Row, Graph, ConsoleCrumb, UserMenu });
