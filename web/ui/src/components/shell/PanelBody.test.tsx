import { render, screen } from "@testing-library/react";
import { PanelBody } from "./PanelBody";

test("renders children inside the standard padded/scrollable wrapper", () => {
  render(
    <PanelBody>
      <div>PANEL-BODY-CONTENT</div>
    </PanelBody>,
  );

  const content = screen.getByText("PANEL-BODY-CONTENT");
  expect(content).toBeInTheDocument();

  const wrapper = content.parentElement;
  expect(wrapper?.className).toMatch(/p-4/);
  expect(wrapper?.className).toMatch(/space-y-4/);
  expect(wrapper?.className).toMatch(/overflow-y-auto/);
});

test("merges an extra className onto the wrapper", () => {
  render(
    <PanelBody className="custom-extra">
      <div>X</div>
    </PanelBody>,
  );
  expect(screen.getByText("X").parentElement?.className).toMatch(/custom-extra/);
});
