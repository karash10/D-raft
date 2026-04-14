import React from 'react';


const COLORS = [
  '#ef4444',
  '#f97316',
  '#f59e0b',
  '#22c55e',
  '#06b6d4',
  '#3b82f6',
  '#a855f7',
  '#ec4899',
  '#000000',
];

const PRESET_WIDTHS = [2, 4, 8, 12, 16];

export type ToolbarProps = {
  selectedColor: string;
  onSelectColor: (color: string) => void;
  strokeWidth: number;
  onSelectWidth: (width: number) => void;
  isEraser: boolean;
  onToggleEraser: (eraser: boolean) => void;
};

export default function Toolbar({
  selectedColor,
  onSelectColor,
  strokeWidth,
  onSelectWidth,
  isEraser,
  onToggleEraser,
}: ToolbarProps) {
  return (
    <div className="absolute bottom-6 left-1/2 -translate-x-1/2 z-20 flex flex-wrap items-center justify-center gap-4 px-6 py-4 bg-white rounded-xl shadow-lg border border-neutral-200 pointer-events-auto">

      <div className="flex items-center gap-2 border-r border-neutral-200 pr-4">
        <button
          onClick={() => onToggleEraser(false)}
          className={`p-2 rounded-lg transition-colors flex items-center justify-center space-x-2 ${!isEraser ? 'bg-blue-100 text-blue-700' : 'hover:bg-neutral-100 text-neutral-600'
            }`}
          title="Pen"
        >
          <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
          </svg>
        </button>
        <button
          onClick={() => onToggleEraser(true)}
          className={`p-2 rounded-lg transition-colors flex items-center justify-center space-x-2 ${isEraser ? 'bg-blue-100 text-blue-700' : 'hover:bg-neutral-100 text-neutral-600'
            }`}
          title="Eraser"
        >

          <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 14l-7 7m0 0l-7-7m7 7v-11a2 2 0 012-2h4a2 2 0 012 2v1" />
          </svg>
        </button>
      </div>


      <div className={`flex items-center gap-1 ${isEraser ? 'opacity-30 pointer-events-none' : ''} transition-opacity duration-200`}>
        {COLORS.map((color) => (
          <button
            key={color}
            onClick={() => onSelectColor(color)}
            className={`w-8 h-8 rounded-full transition-transform ${selectedColor === color && !isEraser ? 'scale-125 ring-2 ring-offset-2 ring-blue-500' : 'hover:scale-110'
              }`}
            style={{ backgroundColor: color }}
            title={`Select Color ${color}`}
          />
        ))}
      </div>


      <div className="flex items-center gap-2 border-l border-neutral-200 pl-4">
        {PRESET_WIDTHS.map((width) => (
          <button
            key={width}
            onClick={() => onSelectWidth(width)}
            className={`flex items-center justify-center w-8 h-8 rounded-full transition-colors ${strokeWidth === width ? 'bg-neutral-200' : 'hover:bg-neutral-100'
              }`}
            title={`Stroke Width ${width}`}
          >
            <div
              className={`rounded-full ${isEraser ? 'bg-pink-300' : 'bg-neutral-700'}`}
              style={{ width: width + 2, height: width + 2 }}
            />
          </button>
        ))}
      </div>
    </div>
  );
}
