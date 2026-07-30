import { useRef, useState } from "react";
import { Loader2, Upload } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/shell/Toast";
import { ApiError } from "@/lib/api";
import { useUploadKBAsset, useKBAssets } from "@/lib/kb";

// ImagePicker inserts an image into the editor from one of two tabs:
//  - "Upload" — pick a file from the computer, stored under the vault's assets/.
//  - "Knowledge base" — pick an image already stored in the vault.
// Either way it calls onPick(vaultRelativePath); the caller inserts the image
// node with that portable path (see kbImage.ts for the path↔URL mapping).
export default function ImagePicker({
  open,
  onOpenChange,
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPick: (path: string) => void;
}) {
  const [tab, setTab] = useState<"upload" | "kb">("upload");
  const upload = useUploadKBAsset();
  const { data: assets = [], isLoading } = useKBAssets(open);
  const { toast } = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);

  async function handleFile(file: File) {
    try {
      const res = await upload.mutateAsync(file);
      onPick(res.path);
      onOpenChange(false);
    } catch (err) {
      toast({
        message: err instanceof ApiError ? `Couldn't upload: ${err.message}` : "Couldn't upload image",
        variant: "error",
      });
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Insert image</DialogTitle>
        </DialogHeader>

        <div className="mb-3 flex gap-1 border-b border-border">
          <TabButton active={tab === "upload"} onClick={() => setTab("upload")}>Upload</TabButton>
          <TabButton active={tab === "kb"} onClick={() => setTab("kb")}>Knowledge base</TabButton>
        </div>

        {tab === "upload" ? (
          <div
            className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border py-10"
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              const file = e.dataTransfer.files?.[0];
              if (file) void handleFile(file);
            }}
          >
            {upload.isPending ? (
              <Loader2 className="size-6 animate-spin text-muted-2" />
            ) : (
              <>
                <Upload className="size-6 text-muted-2" />
                <p className="text-sm text-muted-2">Drag an image here, or</p>
                <Button variant="outline" size="sm" onClick={() => fileInputRef.current?.click()}>
            <Upload />
                  Choose file
                </Button>
                <input
                  ref={fileInputRef}
                  type="file"
                  accept="image/*"
                  className="sr-only"
                  aria-label="Choose image"
                  onChange={(e) => {
                    const file = e.target.files?.[0];
                    e.target.value = "";
                    if (file) void handleFile(file);
                  }}
                />
              </>
            )}
          </div>
        ) : (
          <div className="max-h-72 overflow-y-auto">
            {isLoading ? (
              <p className="py-6 text-center text-sm text-muted-2">Loading…</p>
            ) : assets.length === 0 ? (
              <p className="py-6 text-center text-sm text-muted-2">No images in the knowledge base yet.</p>
            ) : (
              <div className="grid grid-cols-3 gap-2">
                {assets.map((a) => (
                  <button
                    key={a.path}
                    type="button"
                    title={a.path}
                    onClick={() => {
                      onPick(a.path);
                      onOpenChange(false);
                    }}
                    className="aspect-square overflow-hidden rounded border border-border hover:ring-2 hover:ring-ring"
                  >
                    <img src={a.url} alt={a.path} className="h-full w-full object-cover" />
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        "-mb-px border-b-2 px-3 py-1.5 text-sm " +
        (active ? "border-accent text-foreground" : "border-transparent text-muted-2 hover:text-foreground")
      }
    >
      {children}
    </button>
  );
}
