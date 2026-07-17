// image.ts —— 上传前的图片压缩（对齐 iOS compressForUpload 的口径）：
// 相册原图/截图动辄几 MB，压到最长边 1600px、JPEG 质量 0.7——
// 足够视觉模型看清文字，又不撑爆上传。canvas 顺带把 HEIC 之外的格式统一成 JPEG。
export async function compressForUpload(
  file: File,
  maxDimension = 1600,
  quality = 0.7,
): Promise<{ base64: string; mime: string }> {
  const bitmap = await createImageBitmap(file);
  const scale = Math.min(1, maxDimension / Math.max(bitmap.width, bitmap.height));
  const w = Math.round(bitmap.width * scale);
  const h = Math.round(bitmap.height * scale);

  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("浏览器不支持 canvas");
  // 底色刷白：透明 PNG 转 JPEG 时透明区会变黑，白底才像「纸上的截图」。
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, w, h);
  ctx.drawImage(bitmap, 0, 0, w, h);
  bitmap.close();

  const dataURL = canvas.toDataURL("image/jpeg", quality);
  return { base64: dataURL.split(",")[1], mime: "image/jpeg" };
}
