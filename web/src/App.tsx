import ClusterOverview from "./components/ClusterOverview";
import PodsSection from "./components/PodsSection";
import ConfigSection from "./components/ConfigSection";

function App() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b px-6 py-4">
        <h1 className="text-2xl font-bold">GPU Scheduler Dashboard</h1>
      </header>
      <main className="mx-auto max-w-screen-xl space-y-8 p-6">
        <ClusterOverview />
        <PodsSection />
        <ConfigSection />
      </main>
    </div>
  );
}

export default App;
