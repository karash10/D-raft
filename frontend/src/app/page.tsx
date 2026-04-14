import Canvas from '@/components/Canvas'

export default function Home() {
  return (
    <main className="min-h-screen bg-neutral-50 flex items-center justify-center p-4 selection:bg-blue-100">
      <div className="w-full h-[calc(100vh-2rem)] max-w-7xl max-h-[900px] mt-8 flex-1">
        <Canvas />
      </div>
    </main>
  )
}
