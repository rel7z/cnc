import { Header } from "@/components/dashboard/Header";
import { ProcessorSettings } from "@/components/dashboard/ProcessorSettings";

export default function ProcessorPage() {
  return <>
    <Header title="Processor" />
    <div className="mx-auto max-w-[1500px] p-3 md:p-5"><ProcessorSettings /></div>
  </>;
}
