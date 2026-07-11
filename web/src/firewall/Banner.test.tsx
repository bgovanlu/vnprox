import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { FirewallBanners } from "./Banner";
import type { BannerView } from "../api/types";

describe("FirewallBanners", () => {
  it("renders nothing when there are no banners", () => {
    const { container } = render(<FirewallBanners banners={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when banners is undefined", () => {
    const { container } = render(<FirewallBanners banners={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the documented datacenter-off warning verbatim", () => {
    const banners: BannerView[] = [
      { scope: "cluster", message: "Datacenter firewall is OFF: none of these rules are active." },
    ];
    render(<FirewallBanners banners={banners} />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Datacenter firewall is OFF: none of these rules are active.",
    );
  });

  it("renders every banner when multiple gates stack (cascaded + own-scope)", () => {
    const banners: BannerView[] = [
      { scope: "cluster", message: "Datacenter firewall is OFF: none of node pve1's host-level rules are active." },
      { scope: "node", message: "Firewall is OFF for node pve1: none of its own rules are active." },
    ];
    render(<FirewallBanners banners={banners} />);
    expect(screen.getByText(/Datacenter firewall is OFF/)).toBeInTheDocument();
    expect(screen.getByText(/Firewall is OFF for node pve1/)).toBeInTheDocument();
  });
});
