import "./my-element";

document.querySelector<HTMLBodyElement>('#app')!.innerHTML = `
<my-element>
  <h1>Vite + Lit</h1>
</my-element>
`