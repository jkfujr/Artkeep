declare module 'foliate-js/view.js' {
  export interface TOCItem { label: string; href: string; subitems?: TOCItem[] }
  export interface Book {
    toc?: TOCItem[];
    sections: { id: string; linear?: string }[];
    transformTarget?: EventTarget;
    destroy: () => void;
  }
  export function makeBook(file: File): Promise<Book>;
  export class View extends HTMLElement {
    book: Book;
    renderer: HTMLElement & { setStyles: (css: string) => void; getContents: () => { index: number; doc: Document }[] };
    open: (book: Book) => Promise<void>;
    init: (options: { lastLocation?: string; showTextStart?: boolean }) => Promise<void>;
    goTo: (target: number | string) => Promise<unknown>;
    close: () => void;
  }
}
