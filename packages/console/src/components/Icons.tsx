// packages/console/src/components/Icons.tsx
//
// Lucide wrapper and the Aether mark, ported from docs/design/ui_kits/
// aether-edit/Icons.jsx. The prototype rendered the Lucide UMD icon data by
// hand; here we use lucide-react (ISC) and keep the same call shape
// (<Icon name="..." size sw color fill />) so the ported components read the
// same as the prototype. Names are the kebab-case Lucide ids.

import type { ComponentType, CSSProperties, SVGProps } from "react";
import {
  AudioLines,
  Captions,
  Check,
  ChevronDown,
  ChevronRight,
  Gauge,
  KeyRound,
  Layers,
  Lock,
  LogOut,
  Package,
  Pause,
  Play,
  Plus,
  Receipt,
  RotateCcw,
  ScanLine,
  Upload,
  UploadCloud,
  User,
  Video,
  X,
} from "lucide-react";

type LucideIcon = ComponentType<SVGProps<SVGSVGElement> & { size?: number | string }>;

// Only the glyphs the file console actually renders are registered. The
// prototype-only "unplug" (Drop link fault injector) is intentionally absent
// because that control is removed per R10(b).
const REGISTRY: Record<string, LucideIcon> = {
  "audio-lines": AudioLines,
  captions: Captions,
  check: Check,
  "chevron-down": ChevronDown,
  "chevron-right": ChevronRight,
  gauge: Gauge,
  "key-round": KeyRound,
  layers: Layers,
  lock: Lock,
  "log-out": LogOut,
  package: Package,
  pause: Pause,
  play: Play,
  plus: Plus,
  receipt: Receipt,
  "rotate-ccw": RotateCcw,
  "scan-line": ScanLine,
  upload: Upload,
  "upload-cloud": UploadCloud,
  user: User,
  video: Video,
  x: X,
};

export interface IconProps {
  name: string;
  size?: number;
  sw?: number;
  color?: string;
  style?: CSSProperties;
  fill?: string;
}

export function Icon({ name, size = 18, sw = 1.6, color = "currentColor", style, fill = "none" }: IconProps) {
  const Cmp = REGISTRY[name];
  if (!Cmp) {
    return <svg width={size} height={size} viewBox="0 0 24 24" style={style} />;
  }
  return <Cmp width={size} height={size} strokeWidth={sw} stroke={color} fill={fill} style={style} />;
}

export interface MarkProps {
  size?: number;
  color?: string;
}

// The Aether mark, inline so it can be tinted (ported verbatim).
export function Mark({ size = 20, color = "#fff" }: MarkProps) {
  return (
    <svg width={size} height={size * 0.85} viewBox="0 0 40 34" fill="none">
      <path d="M3 31.4 L20 2.6 L37 31.4" stroke={color} strokeWidth="3.1" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M13.2 31.4 L20 19.6 L26.8 31.4 Z" fill={color} />
    </svg>
  );
}
