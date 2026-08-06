// Icons.jsx — React wrapper over the Lucide UMD icon data (editor needs ~60 glyphs).
// Lucide exposes icon nodes as [tag, attrs] pairs; we render them as React elements
// rather than letting createIcons() mutate the DOM under React.
function Icon({ name, size = 18, sw = 1.6, color = 'currentColor', style, fill = 'none' }) {
  const pascal = name.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
  const node = (window.lucide && (lucide.icons[pascal] || lucide.icons[name])) || null;
  if (!node) {
    return <svg width={size} height={size} viewBox="0 0 24 24" style={style} />;
  }
  // Lucide UMD nodes are ["svg", attrs, children]; older shapes are a bare child list.
  const children = (Array.isArray(node) && node[0] === 'svg' && Array.isArray(node[2]))
    ? node[2]
    : (Array.isArray(node) ? node : []);
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill={fill} stroke={color}
      strokeWidth={sw} strokeLinecap="round" strokeLinejoin="round" style={style}>
      {children.map(([tag, attrs], i) => {
        const p = { key: i };
        for (const k in attrs) {
          if (k === 'key') continue;
          const rk = k.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
          p[rk] = attrs[k];
        }
        return React.createElement(tag, p);
      })}
    </svg>
  );
}

// The Aether mark, inline so it can be tinted.
function Mark({ size = 20, color = '#fff' }) {
  return (
    <svg width={size} height={size * 0.85} viewBox="0 0 40 34" fill="none">
      <path d="M3 31.4 L20 2.6 L37 31.4" stroke={color} strokeWidth="3.1" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M13.2 31.4 L20 19.6 L26.8 31.4 Z" fill={color} />
    </svg>
  );
}
Object.assign(window, { Icon, Mark });
