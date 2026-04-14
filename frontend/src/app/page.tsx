import Canvas from '@/components/Canvas'
import Dashboard from '@/components/Dashboard'

export default function Home() {
  return (
    <main className="min-h-screen bg-neutral-50 flex items-center justify-center p-4 selection:bg-blue-100 relative">
      <Dashboard />
      <div className="w-full h-[calc(100vh-2rem)] max-w-7xl max-h-[900px] mt-8 flex-1 border border-neutral-200/50 bg-white shadow-xl rounded-2xl overflow-hidden">
        <Canvas />
      </div>
    </main>
  )
}
