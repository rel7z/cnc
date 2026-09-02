import { Header } from "@/components/dashboard/Header";
import { SubmitJobForm } from "@/components/dashboard/SubmitJobForm";

export default function NewJobPage() {
  return (
    <>
      <Header title="Submit Job" />
      <div className="p-6">
        <SubmitJobForm />
      </div>
    </>
  );
}
