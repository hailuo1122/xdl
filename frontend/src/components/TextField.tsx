import { forwardRef, type InputHTMLAttributes, type TextareaHTMLAttributes } from "react";

export const TextField = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function TextField(
  { className = "", ...props },
  ref,
) {
  return <input ref={ref} {...props} className={`xdl-field ${className}`.trim()} />;
});

export const TextArea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function TextArea(
  { className = "", ...props },
  ref,
) {
  return <textarea ref={ref} {...props} className={`xdl-field xdl-area ${className}`.trim()} />;
});
